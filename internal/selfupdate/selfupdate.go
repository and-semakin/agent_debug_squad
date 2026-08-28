package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	checksumsAsset    = "checksums.txt"
	binaryName        = "agent-debug-squad"
	maxDownloadSize   = 256 << 20
)

// Client discovers and installs releases published by GoReleaser.
type Client struct {
	Owner      string
	Repository string
	Version    string
	HTTPClient *http.Client
	APIBaseURL string
	GOOS       string
	GOARCH     string
}

// Release contains the downloadable files needed to install one release.
type Release struct {
	Version      string
	AssetName    string
	AssetURL     string
	ChecksumsURL string
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Check returns the latest compatible release when it is newer than Version.
func (c Client) Check(ctx context.Context) (*Release, error) {
	current, err := parseVersion(c.Version)
	if err != nil {
		return nil, fmt.Errorf("current version %q is not a release version: %w", c.Version, err)
	}

	apiBaseURL := strings.TrimRight(c.APIBaseURL, "/")
	if apiBaseURL == "" {
		apiBaseURL = defaultAPIBaseURL
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBaseURL, c.Owner, c.Repository)

	var latest githubRelease
	if err := c.getJSON(ctx, endpoint, &latest); err != nil {
		return nil, fmt.Errorf("check latest release: %w", err)
	}
	if latest.Draft || latest.Prerelease {
		return nil, nil
	}

	candidate, err := parseVersion(latest.TagName)
	if err != nil {
		return nil, fmt.Errorf("latest release tag %q is invalid: %w", latest.TagName, err)
	}
	if !candidate.newerThan(current) {
		return nil, nil
	}

	goos := c.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := c.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos != "darwin" && goos != "linux" {
		return nil, fmt.Errorf("automatic updates are not supported on %s/%s", goos, goarch)
	}

	assetName := fmt.Sprintf("%s_%s_%s.tar.gz", binaryName, goos, goarch)
	var assetURL, checksumsURL string
	for _, asset := range latest.Assets {
		switch asset.Name {
		case assetName:
			assetURL = asset.BrowserDownloadURL
		case checksumsAsset:
			checksumsURL = asset.BrowserDownloadURL
		}
	}
	if assetURL == "" {
		return nil, fmt.Errorf("release %s has no asset %s", latest.TagName, assetName)
	}
	if checksumsURL == "" {
		return nil, fmt.Errorf("release %s has no asset %s", latest.TagName, checksumsAsset)
	}

	return &Release{
		Version:      latest.TagName,
		AssetName:    assetName,
		AssetURL:     assetURL,
		ChecksumsURL: checksumsURL,
	}, nil
}

// IsReleaseVersion reports whether value is a supported vMAJOR.MINOR.PATCH version.
func IsReleaseVersion(value string) bool {
	_, err := parseVersion(value)
	return err == nil
}

// Install downloads, verifies, and atomically replaces executablePath.
func (c Client) Install(ctx context.Context, release Release, executablePath string) error {
	checksums, err := c.download(ctx, release.ChecksumsURL)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	expected, err := checksumFor(checksums, release.AssetName)
	if err != nil {
		return err
	}

	archive, err := c.download(ctx, release.AssetURL)
	if err != nil {
		return fmt.Errorf("download %s: %w", release.AssetName, err)
	}
	actual := sha256.Sum256(archive)
	if !strings.EqualFold(expected, hex.EncodeToString(actual[:])) {
		return fmt.Errorf("checksum mismatch for %s", release.AssetName)
	}

	binary, mode, err := extractBinary(archive)
	if err != nil {
		return fmt.Errorf("extract %s: %w", release.AssetName, err)
	}
	return replaceExecutable(executablePath, binary, mode)
}

func (c Client) getJSON(ctx context.Context, url string, destination any) error {
	body, err := c.download(ctx, url)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c Client) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", binaryName+"/"+c.Version)

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, fmt.Errorf("GET %s: %s", url, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxDownloadSize {
		return nil, fmt.Errorf("download exceeds %d bytes", maxDownloadSize)
	}
	return body, nil
}

func checksumFor(contents []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == assetName {
			if len(fields[0]) != sha256.Size*2 {
				return "", fmt.Errorf("invalid checksum for %s", assetName)
			}
			if _, err := hex.DecodeString(fields[0]); err != nil {
				return "", fmt.Errorf("invalid checksum for %s: %w", assetName, err)
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksums do not contain %s", assetName)
}

func extractBinary(contents []byte) ([]byte, os.FileMode, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(contents))
	if err != nil {
		return nil, 0, err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != binaryName {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tarReader, maxDownloadSize+1))
		if err != nil {
			return nil, 0, err
		}
		if len(body) > maxDownloadSize {
			return nil, 0, fmt.Errorf("executable exceeds %d bytes", maxDownloadSize)
		}
		mode := header.FileInfo().Mode().Perm()
		if mode&0o111 == 0 {
			mode |= 0o755
		}
		return body, mode, nil
	}
	return nil, 0, fmt.Errorf("archive does not contain %s", binaryName)
}

func replaceExecutable(executablePath string, contents []byte, mode os.FileMode) error {
	resolvedPath, err := filepath.EvalSymlinks(executablePath)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(resolvedPath), ".agent-debug-squad-update-*")
	if err != nil {
		return fmt.Errorf("create temporary executable: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary executable: %w", err)
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set executable permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary executable: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary executable: %w", err)
	}
	if err := os.Rename(temporaryPath, resolvedPath); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	return nil
}

type semanticVersion struct {
	major int
	minor int
	patch int
}

func parseVersion(value string) (semanticVersion, error) {
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return semanticVersion{}, fmt.Errorf("expected vMAJOR.MINOR.PATCH")
	}
	values := make([]int, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semanticVersion{}, fmt.Errorf("invalid numeric component %q", part)
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return semanticVersion{}, fmt.Errorf("invalid numeric component %q", part)
			}
		}
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return semanticVersion{}, fmt.Errorf("invalid numeric component %q", part)
		}
		values[index] = parsed
	}
	return semanticVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func (v semanticVersion) newerThan(other semanticVersion) bool {
	if v.major != other.major {
		return v.major > other.major
	}
	if v.minor != other.minor {
		return v.minor > other.minor
	}
	return v.patch > other.patch
}
