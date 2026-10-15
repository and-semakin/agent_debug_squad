package devin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
)

func TestEnvironmentIsExplicitAndConsistent(t *testing.T) {
	ambient := []string{"HOME=/native", "PATH=/bin", "XDG_DATA_HOME=/data", "HTTP_PROXY=http://ambient", "WINDSURF_API_KEY=secret", "DEVIN_REFUSAL_FALLBACK=1"}
	spec := domain.AgentSpec{ListOptions: map[string][]string{"inherit_env": {"HTTP_PROXY"}, "env": {"HOME=/chosen", "HTTP_PROXY=http://explicit"}}}
	got := BuildEnv(spec, ambient)
	want := []string{"HOME=/chosen", "HTTP_PROXY=http://explicit", "PATH=/bin", "XDG_DATA_HOME=/data"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if e := BuildEnv(domain.AgentSpec{}, nil); e == nil || len(e) != 0 {
		t.Fatal("nil means implicit ambient")
	}
}
func TestProfilePreservesPolicyWithoutMutation(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".config")
	os.MkdirAll(filepath.Join(base, "devin"), 0700)
	source := filepath.Join(base, "devin", "config.json")
	original := []byte(`{"auto_update":true,"proxy":{"mode":"off"},"permissions":{"deny":["Exec(sudo)"]},"read_config_from":{"claude":false},"agent":{"model":"native"}}`)
	os.WriteFile(source, original, 0600)
	path, cleanup, e := profile([]string{"HOME=" + home}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	b, _ := os.ReadFile(path)
	var v map[string]any
	json.Unmarshal(b, &v)
	if v["auto_update"] != false || v["subagents_enabled"] != false || !strings.Contains(string(b), "Exec(sudo)") || !strings.Contains(string(b), `"mode":"off"`) || !strings.Contains(string(b), `"claude":false`) {
		t.Fatal("policy lost")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("private profile permissions")
	}
	info, _ = os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0700 {
		t.Fatal("private directory permissions")
	}
	after, _ := os.ReadFile(source)
	if string(after) != string(original) {
		t.Fatal("native config changed")
	}
	cleanup()
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("profile leaked")
	}
}
func TestModelCatalogProjection(t *testing.T) {
	for _, tc := range []struct {
		body, status string
		n            int
	}{
		{`{"families":[{"family_uid":"swe-2","slug":"swe","variants":[{"model_uid":"swe-2-high","label":"SWE High","max_context_tokens":262000,"api_key":"SECRET","raw":"SECRET"}]}]}`, "ok", 1},
		{`{"families":[]}`, "empty", 0}, {`{}`, "unsupported", 0}, {`{"models":[]}`, "unsupported", 0}, {`{"families":[{}]}`, "partial", 0}, {`{"families":[{"variants":[{}]}]}`, "partial", 0}, {`{"families":`, "unsupported", 0},
	} {
		r := parseModels([]byte(tc.body), domain.ModelDiscoveryInput{IncludeHidden: true})
		modelprobe.Finish(&r)
		if r.Status != tc.status || len(r.Models) != tc.n {
			t.Fatalf("%s: %+v", tc.body, r)
		}
		if r.InferenceVerification != "not_checked" || r.Sources[0].HiddenPolicy.Applied != "unknown" || r.Sources[0].Freshness.State != "unknown" {
			t.Fatal("inferred unsupported metadata")
		}
		if tc.n > 0 {
			m := r.Models[0]
			if m.Selection.Supported || len(m.Selection.Options) != 0 || m.ModelID != "swe-2-high" || len(m.Parameters) > 0 {
				t.Fatal("invented family/reasoning selection")
			}
		}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "SECRET") {
			t.Fatal("raw metadata leaked")
		}
	}
}
func TestCatalogLaunchCreatesNoSession(t *testing.T) {
	a, s := fixtureAdapter(t, "ok")
	r := a.ListModels(context.Background(), domain.ModelDiscoveryInput{WorkspaceDir: s.WorkspaceDir, AmbientEnv: os.Environ()})
	if r.Status != "ok" || len(r.Models) != 1 {
		t.Fatal(r)
	}
	if len(calls(t, s.WorkspaceDir, "session/new")) > 0 {
		t.Fatal("catalog created a conversation")
	}
}
func TestInstallationNeverExecutes(t *testing.T) {
	a, s := fixtureAdapter(t, "ok")
	r := a.CheckInstallation(context.Background(), domain.InstallationInput{WorkspaceDir: s.WorkspaceDir, AmbientEnv: os.Environ()})
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), "ready") {
		t.Fatal(r)
	}
	if _, e := os.Stat(filepath.Join(s.WorkspaceDir, "calls.jsonl")); !os.IsNotExist(e) {
		t.Fatal("preflight executed")
	}
}

func TestInstallationUsesEffectivePathAndOfficialLink(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "devin")
	os.WriteFile(bin, []byte("#!/bin/sh\nexit 99\n"), 0700)
	a := New(domain.AgentSpec{Backend: "devin", StringOptions: map[string]string{"command": "devin"}, ListOptions: map[string][]string{"env": {"PATH=" + dir}}})
	r := a.CheckInstallation(context.Background(), domain.InstallationInput{WorkspaceDir: dir, AmbientEnv: []string{"PATH=/missing"}})
	if r.Status != domain.InstallationStatusReady {
		t.Fatal(r)
	}
	a.spec.StringOptions["command"] = "./devin"
	r = a.CheckInstallation(context.Background(), domain.InstallationInput{WorkspaceDir: dir})
	if r.Status != domain.InstallationStatusReady {
		t.Fatal(r)
	}
	a.spec.StringOptions["command"] = "./missing"
	r = a.CheckInstallation(context.Background(), domain.InstallationInput{WorkspaceDir: dir})
	if r.Status != domain.InstallationStatusFailed || r.Issues[0].Code != domain.CodeNotFound || r.Issues[0].InstallationLinks[0].URL != domain.DocDevinCLI {
		t.Fatal(r)
	}
	a.spec.StringOptions["command"] = bin
	os.Chmod(bin, 0600)
	r = a.CheckInstallation(context.Background(), domain.InstallationInput{WorkspaceDir: dir})
	if r.Status != domain.InstallationStatusFailed || r.Issues[0].Code != domain.CodeNotExecutable {
		t.Fatal(r)
	}
}
