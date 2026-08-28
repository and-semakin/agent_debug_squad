package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckReturnsNewCompatibleRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/and-semakin/agent_debug_squad/releases/latest" {
			t.Fatalf("unexpected request path %q", request.URL.Path)
		}
		fmt.Fprint(response, `{
			"tag_name":"v1.3.0",
			"assets":[
				{"name":"agent-debug-squad_darwin_arm64.tar.gz","browser_download_url":"https://example.test/binary"},
				{"name":"checksums.txt","browser_download_url":"https://example.test/checksums"}
			]
		}`)
	}))
	defer server.Close()

	client := Client{
		Owner:      "and-semakin",
		Repository: "agent_debug_squad",
		Version:    "v1.2.3",
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
		GOOS:       "darwin",
		GOARCH:     "arm64",
	}
	release, err := client.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if release == nil {
		t.Fatal("Check returned no release")
	}
	if release.Version != "v1.3.0" || release.AssetName != "agent-debug-squad_darwin_arm64.tar.gz" {
		t.Fatalf("unexpected release: %#v", release)
	}
}

func TestCheckReturnsNilWhenCurrentVersionIsLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `{"tag_name":"v1.2.3"}`)
	}))
	defer server.Close()

	client := Client{
		Owner:      "and-semakin",
		Repository: "agent_debug_squad",
		Version:    "v1.2.3",
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
	}
	release, err := client.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if release != nil {
		t.Fatalf("expected no release, got %#v", release)
	}
}

func TestCheckRejectsNonReleaseVersionWithoutNetworkRequest(t *testing.T) {
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requested = true
	}))
	defer server.Close()

	client := Client{
		Owner:      "and-semakin",
		Repository: "agent_debug_squad",
		Version:    "dev",
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
	}
	if _, err := client.Check(context.Background()); err == nil {
		t.Fatal("Check succeeded for a development version")
	}
	if requested {
		t.Fatal("Check made a network request for a development version")
	}
}

func TestInstallVerifiesArchiveAndReplacesExecutable(t *testing.T) {
	archive := makeArchive(t, []byte("new binary"))
	sum := sha256.Sum256(archive)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/checksums.txt":
			fmt.Fprintf(response, "%x  agent-debug-squad_darwin_arm64.tar.gz\n", sum)
		case "/archive.tar.gz":
			response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	executablePath := filepath.Join(directory, binaryName)
	if err := os.WriteFile(executablePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := Client{Version: "v1.0.0", HTTPClient: server.Client()}
	err := client.Install(context.Background(), Release{
		Version:      "v1.1.0",
		AssetName:    "agent-debug-squad_darwin_arm64.tar.gz",
		AssetURL:     server.URL + "/archive.tar.gz",
		ChecksumsURL: server.URL + "/checksums.txt",
	}, executablePath)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	contents, err := os.ReadFile(executablePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "new binary" {
		t.Fatalf("executable contents = %q", contents)
	}
	info, err := os.Stat(executablePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replacement is not executable: %v", info.Mode())
	}
}

func TestInstallLeavesExecutableUntouchedOnChecksumMismatch(t *testing.T) {
	archive := makeArchive(t, []byte("new binary"))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/checksums.txt":
			fmt.Fprintf(response, "%064x  archive.tar.gz\n", 0)
		case "/archive.tar.gz":
			response.Write(archive)
		}
	}))
	defer server.Close()

	executablePath := filepath.Join(t.TempDir(), binaryName)
	if err := os.WriteFile(executablePath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := Client{Version: "v1.0.0", HTTPClient: server.Client()}
	err := client.Install(context.Background(), Release{
		AssetName:    "archive.tar.gz",
		AssetURL:     server.URL + "/archive.tar.gz",
		ChecksumsURL: server.URL + "/checksums.txt",
	}, executablePath)
	if err == nil {
		t.Fatal("Install succeeded with a mismatched checksum")
	}
	contents, readErr := os.ReadFile(executablePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(contents) != "old binary" {
		t.Fatalf("executable was changed to %q", contents)
	}
}

func TestParseVersionComparison(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		newer     bool
	}{
		{candidate: "v2.0.0", current: "v1.99.99", newer: true},
		{candidate: "1.3.0", current: "v1.2.9", newer: true},
		{candidate: "v1.2.3", current: "v1.2.3", newer: false},
		{candidate: "v1.2.2", current: "v1.2.3", newer: false},
	}
	for _, test := range tests {
		candidate, err := parseVersion(test.candidate)
		if err != nil {
			t.Fatalf("parse candidate %q: %v", test.candidate, err)
		}
		current, err := parseVersion(test.current)
		if err != nil {
			t.Fatalf("parse current %q: %v", test.current, err)
		}
		if got := candidate.newerThan(current); got != test.newer {
			t.Errorf("%s newer than %s = %v, want %v", test.candidate, test.current, got, test.newer)
		}
	}
}

func TestParseVersionRejectsNonReleaseVersions(t *testing.T) {
	for _, value := range []string{"dev", "v1.2", "v1.2.3-beta.1", "v1.02.3", "v+1.2.3", "vv1.2.3"} {
		if _, err := parseVersion(value); err == nil {
			t.Errorf("parseVersion(%q) succeeded", value)
		}
	}
}

func makeArchive(t *testing.T, contents []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{
		Name:     binaryName,
		Mode:     0o755,
		Size:     int64(len(contents)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
