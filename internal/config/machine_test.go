package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func writeMachineBackends(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agent-debug-squad"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(MachineBackendsPath(home), []byte(content), 0o600); err != nil {
		t.Fatalf("write backends config: %v", err)
	}
	return home
}

func TestLoadMachineBackendsMissingFileIsEmpty(t *testing.T) {
	backends, err := LoadMachineBackends(t.TempDir())
	if err != nil {
		t.Fatalf("LoadMachineBackends() error = %v", err)
	}
	if names := backends.Configured(); len(names) != 0 {
		t.Fatalf("expected no configured backends, got %v", names)
	}
}

func TestLoadMachineBackendsEmptyFileIsValid(t *testing.T) {
	home := writeMachineBackends(t, "\n   \n")
	backends, err := LoadMachineBackends(home)
	if err != nil {
		t.Fatalf("LoadMachineBackends() error = %v", err)
	}
	if names := backends.Configured(); len(names) != 0 {
		t.Fatalf("expected no configured backends, got %v", names)
	}
}

func TestLoadMachineBackendsEverySupportedKey(t *testing.T) {
	home := writeMachineBackends(t, `
codex:
  command: /opt/codex
  proxy_url: http://proxy.example:8080
  no_proxy: localhost,127.0.0.1
cursor:
  command: /opt/cursor-agent
  proxy_url: https://proxy.example:8443
  no_proxy:
    - localhost
    - ".internal.example"
  ca_cert_file: /etc/cursor-ca.pem
kimi:
  command: /opt/kimi
  proxy_url: http://proxy.example:8080
zcode:
  command: /opt/node
  runtime_path: /opt/zcode.cjs
  proxy_url: http://proxy.example:8080
  no_proxy: localhost
  ca_cert_file: /etc/zcode-ca.pem
opencode:
  mode: external
  base_url: http://127.0.0.1:4097
judge:
  proxy_url: http://proxy.example:8080
`)
	backends, err := LoadMachineBackends(home)
	if err != nil {
		t.Fatalf("LoadMachineBackends() error = %v", err)
	}
	want := domain.MachineBackends{
		Codex:    &domain.MachineBackendSettings{Command: "/opt/codex", ProxyURL: "http://proxy.example:8080", NoProxy: "localhost,127.0.0.1"},
		Cursor:   &domain.MachineBackendSettings{Command: "/opt/cursor-agent", ProxyURL: "https://proxy.example:8443", NoProxy: "localhost,.internal.example", CACertFile: "/etc/cursor-ca.pem"},
		Kimi:     &domain.MachineBackendSettings{Command: "/opt/kimi", ProxyURL: "http://proxy.example:8080"},
		ZCode:    &domain.MachineBackendSettings{Command: "/opt/node", RuntimePath: "/opt/zcode.cjs", ProxyURL: "http://proxy.example:8080", NoProxy: "localhost", CACertFile: "/etc/zcode-ca.pem"},
		OpenCode: &domain.MachineBackendSettings{Mode: "external", BaseURL: "http://127.0.0.1:4097", Declared: map[string]bool{"mode": true, "base_url": true}},
		Judge:    &domain.MachineBackendSettings{ProxyURL: "http://proxy.example:8080"},
	}
	if !reflect.DeepEqual(backends.Codex, want.Codex) {
		t.Errorf("codex = %+v, want %+v", backends.Codex, want.Codex)
	}
	if !reflect.DeepEqual(backends.Cursor, want.Cursor) {
		t.Errorf("cursor = %+v, want %+v", backends.Cursor, want.Cursor)
	}
	if !reflect.DeepEqual(backends.Kimi, want.Kimi) {
		t.Errorf("kimi = %+v, want %+v", backends.Kimi, want.Kimi)
	}
	if !reflect.DeepEqual(backends.ZCode, want.ZCode) {
		t.Errorf("zcode = %+v, want %+v", backends.ZCode, want.ZCode)
	}
	if !reflect.DeepEqual(backends.OpenCode, want.OpenCode) {
		t.Errorf("opencode = %+v, want %+v", backends.OpenCode, want.OpenCode)
	}
	if !reflect.DeepEqual(backends.Judge, want.Judge) {
		t.Errorf("judge = %+v, want %+v", backends.Judge, want.Judge)
	}
	if got, want := backends.Configured(), []string{"codex", "cursor", "judge", "kimi", "opencode", "zcode"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Configured() = %v, want %v", got, want)
	}
}

func TestLoadMachineBackendsEmptySectionIsNotConfigured(t *testing.T) {
	home := writeMachineBackends(t, "codex:\n")
	backends, err := LoadMachineBackends(home)
	if err != nil {
		t.Fatalf("LoadMachineBackends() error = %v", err)
	}
	if backends.Codex != nil {
		t.Fatalf("expected empty codex section to be unconfigured, got %+v", backends.Codex)
	}
}

func TestLoadMachineJudgeConfidenceThreshold(t *testing.T) {
	home := writeMachineBackends(t, "judge:\n  proxy_url: http://proxy.example:8080\n  confidence_threshold: 0.6\n")
	backends, err := LoadMachineBackends(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := backends.JudgeConfidenceThreshold(); got != 0.6 {
		t.Fatalf("judge confidence threshold = %v, want 0.6", got)
	}
	if got := backends.Configured(); !reflect.DeepEqual(got, []string{"judge"}) {
		t.Fatalf("configured sections = %v", got)
	}
	if backends.JudgeProxyURL() != "http://proxy.example:8080" {
		t.Fatalf("judge proxy setting was not preserved")
	}
}

func TestLoadMachineJudgeConfidenceThresholdRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "-0.1", "1.1", "null", "true", "\"0.6\"", ".nan", ".inf", "[]"} {
		t.Run(value, func(t *testing.T) {
			home := writeMachineBackends(t, "judge:\n  confidence_threshold: "+value+"\n")
			_, err := LoadMachineBackends(home)
			if err == nil || !strings.Contains(err.Error(), MachineBackendsPath(home)) ||
				!strings.Contains(err.Error(), "judge") || !strings.Contains(err.Error(), "confidence_threshold") ||
				!strings.Contains(err.Error(), "greater than 0 and at most 1") {
				t.Fatalf("invalid threshold %q: %v", value, err)
			}
		})
	}
	for _, value := range []string{"0.6", "1"} {
		t.Run("valid_"+value, func(t *testing.T) {
			home := writeMachineBackends(t, "judge:\n  confidence_threshold: "+value+"\n")
			if _, err := LoadMachineBackends(home); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, section := range []string{"codex", "cursor", "kimi", "opencode", "zcode"} {
		t.Run("unsupported_"+section, func(t *testing.T) {
			home := writeMachineBackends(t, section+":\n  confidence_threshold: 0.6\n")
			_, err := LoadMachineBackends(home)
			if err == nil || !strings.Contains(err.Error(), "unsupported key") || !strings.Contains(err.Error(), "confidence_threshold") {
				t.Fatalf("section %s: %v", section, err)
			}
		})
	}
}

func TestLoadMachineBackendsUnknownSectionFails(t *testing.T) {
	home := writeMachineBackends(t, "claude:\n  command: /opt/claude\n")
	_, err := LoadMachineBackends(home)
	if err == nil {
		t.Fatal("expected unknown section error")
	}
	path := MachineBackendsPath(home)
	for _, want := range []string{path, "claude", "codex", "cursor", "judge", "kimi", "opencode", "zcode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestLoadMachineBackendsUnsupportedKeyFails(t *testing.T) {
	home := writeMachineBackends(t, "opencode:\n  ca_cert_file: /tmp/ca.pem\n")
	_, err := LoadMachineBackends(home)
	if err == nil {
		t.Fatal("expected unsupported key error")
	}
	path := MachineBackendsPath(home)
	for _, want := range []string{path, "opencode", "ca_cert_file", "base_url"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestLoadMachineBackendsInvalidProxyURLFails(t *testing.T) {
	home := writeMachineBackends(t, "cursor:\n  proxy_url: \"not a url\"\n")
	_, err := LoadMachineBackends(home)
	if err == nil || !strings.Contains(err.Error(), "proxy_url") || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("expected invalid proxy_url error naming key and section, got %v", err)
	}

	home = writeMachineBackends(t, "cursor:\n  proxy_url: ftp://proxy.example:21\n")
	if _, err := LoadMachineBackends(home); err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("expected scheme error, got %v", err)
	}
}

func TestLoadMachineBackendsWrongValueTypeFails(t *testing.T) {
	home := writeMachineBackends(t, "codex:\n  command: [\"a\", \"b\"]\n")
	_, err := LoadMachineBackends(home)
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("expected wrong type error naming key, got %v", err)
	}

	home = writeMachineBackends(t, "codex:\n  no_proxy: {a: b}\n")
	if _, err := LoadMachineBackends(home); err == nil || !strings.Contains(err.Error(), "no_proxy") {
		t.Fatalf("expected no_proxy type error, got %v", err)
	}
}

func TestMachineEnvEntriesPerBackend(t *testing.T) {
	settings := &domain.MachineBackendSettings{
		ProxyURL:   "http://proxy.example:8080",
		NoProxy:    "localhost,.internal.example",
		CACertFile: "/etc/ca.pem",
	}
	cases := []struct {
		backend string
		want    []string
	}{
		{
			backend: "codex",
			want: []string{
				"HTTP_PROXY=http://proxy.example:8080",
				"HTTPS_PROXY=http://proxy.example:8080",
				"NO_PROXY=localhost,.internal.example",
			},
		},
		{
			backend: "kimi",
			want: []string{
				"HTTP_PROXY=http://proxy.example:8080",
				"HTTPS_PROXY=http://proxy.example:8080",
				"NODE_USE_ENV_PROXY=1",
				"NO_PROXY=localhost,.internal.example",
			},
		},
		{
			backend: "cursor",
			want: []string{
				"HTTP_PROXY=http://proxy.example:8080",
				"HTTPS_PROXY=http://proxy.example:8080",
				"NODE_USE_ENV_PROXY=1",
				"NO_PROXY=localhost,.internal.example",
				"NODE_EXTRA_CA_CERTS=/etc/ca.pem",
			},
		},
		{
			backend: "zcode",
			want: []string{
				"ZCODE_HTTP_PROXY=http://proxy.example:8080",
				"ZCODE_NO_PROXY=localhost,.internal.example",
				"ZCODE_AGENT_CA_CERT=/etc/ca.pem",
			},
		},
		{backend: "opencode", want: nil},
		{backend: "judge", want: nil},
		{backend: "unknown", want: nil},
	}
	for _, tc := range cases {
		got := MachineEnvEntries(tc.backend, settings)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s entries = %v, want %v", tc.backend, got, tc.want)
		}
	}
}

func TestMachineEnvEntriesOmitUnsetAndNeverLeakPlainProxyToZCode(t *testing.T) {
	entries := MachineEnvEntries("codex", &domain.MachineBackendSettings{NoProxy: "localhost"})
	if len(entries) != 1 || entries[0] != "NO_PROXY=localhost" {
		t.Fatalf("codex entries = %v, want only NO_PROXY", entries)
	}
	entries = MachineEnvEntries("zcode", &domain.MachineBackendSettings{ProxyURL: "http://proxy.example:8080"})
	for _, entry := range entries {
		if strings.HasPrefix(entry, "HTTP_PROXY=") || strings.HasPrefix(entry, "HTTPS_PROXY=") {
			t.Fatalf("zcode must not receive machine-derived plain proxy vars, got %v", entries)
		}
	}
	if len(entries) != 1 || entries[0] != "ZCODE_HTTP_PROXY=http://proxy.example:8080" {
		t.Fatalf("zcode entries = %v, want only ZCODE_HTTP_PROXY", entries)
	}
	if MachineEnvEntries("codex", nil) != nil {
		t.Fatal("nil settings must yield no entries")
	}
}

func agentSpecForMerge(backend string) domain.AgentSpec {
	return domain.AgentSpec{
		Name:          "Agent",
		Backend:       backend,
		StartupPrompt: "start",
		StringOptions: map[string]string{},
		ListOptions:   map[string][]string{},
	}
}

func TestMergeMachineDefaultsAppliesCommandAndEnv(t *testing.T) {
	backends := domain.MachineBackends{
		Codex: &domain.MachineBackendSettings{Command: "/opt/codex", ProxyURL: "http://proxy.example:8080"},
	}
	spec := agentSpecForMerge("codex")
	merged := MergeMachineDefaults(spec, backends)
	if got := merged.StringOptions["command"]; got != "/opt/codex" {
		t.Fatalf("command = %q, want machine default", got)
	}
	wantEnv := []string{
		"HTTP_PROXY=http://proxy.example:8080",
		"HTTPS_PROXY=http://proxy.example:8080",
	}
	if !reflect.DeepEqual(merged.ListOptions["env"], wantEnv) {
		t.Fatalf("env = %v, want %v", merged.ListOptions["env"], wantEnv)
	}
}

func TestMergeMachineDefaultsAgentCommandWins(t *testing.T) {
	backends := domain.MachineBackends{
		Kimi: &domain.MachineBackendSettings{Command: "/opt/kimi"},
	}
	spec := agentSpecForMerge("kimi")
	spec.StringOptions["command"] = "/custom/kimi"
	merged := MergeMachineDefaults(spec, backends)
	if got := merged.StringOptions["command"]; got != "/custom/kimi" {
		t.Fatalf("command = %q, want agent value", got)
	}
}

func TestMergeMachineDefaultsExplicitEnvWins(t *testing.T) {
	backends := domain.MachineBackends{
		Codex: &domain.MachineBackendSettings{ProxyURL: "http://proxy.example:8080"},
	}
	spec := agentSpecForMerge("codex")
	spec.ListOptions["env"] = []string{"HTTPS_PROXY=http://direct.example:3128", "EXTRA=1"}
	merged := MergeMachineDefaults(spec, backends)
	// HTTPS_PROXY was defined explicitly and must stay the agent's value with
	// no duplicate machine entry; HTTP_PROXY is still injected.
	wantEnv := []string{"HTTP_PROXY=http://proxy.example:8080", "HTTPS_PROXY=http://direct.example:3128", "EXTRA=1"}
	if !reflect.DeepEqual(merged.ListOptions["env"], wantEnv) {
		t.Fatalf("env = %v, want %v", merged.ListOptions["env"], wantEnv)
	}
	if hasDuplicateKeys(merged.ListOptions["env"]) {
		t.Fatalf("env has duplicate keys: %v", merged.ListOptions["env"])
	}
}

func TestMergeMachineDefaultsInheritEnvNamingWins(t *testing.T) {
	backends := domain.MachineBackends{
		Cursor: &domain.MachineBackendSettings{ProxyURL: "http://proxy.example:8080"},
	}
	spec := agentSpecForMerge("cursor")
	spec.ListOptions["inherit_env"] = []string{"HTTPS_PROXY", "HOME"}
	merged := MergeMachineDefaults(spec, backends)
	wantEnv := []string{"HTTP_PROXY=http://proxy.example:8080", "NODE_USE_ENV_PROXY=1"}
	if !reflect.DeepEqual(merged.ListOptions["env"], wantEnv) {
		t.Fatalf("env = %v, want %v (HTTPS_PROXY suppressed for inherit_env)", merged.ListOptions["env"], wantEnv)
	}
}

func TestMergeMachineDefaultsDoesNotMutateInput(t *testing.T) {
	backends := domain.MachineBackends{
		ZCode: &domain.MachineBackendSettings{Command: "/opt/node", RuntimePath: "/opt/zcode.cjs", ProxyURL: "http://proxy.example:8080"},
	}
	spec := agentSpecForMerge("zcode")
	spec.ListOptions["env"] = []string{"K=v"}
	merged := MergeMachineDefaults(spec, backends)
	if got := spec.StringOptions["command"]; got != "" {
		t.Fatalf("input spec command mutated to %q", got)
	}
	if !reflect.DeepEqual(spec.ListOptions["env"], []string{"K=v"}) {
		t.Fatalf("input spec env mutated: %v", spec.ListOptions["env"])
	}
	if got := merged.StringOptions["runtime_path"]; got != "/opt/zcode.cjs" {
		t.Fatalf("runtime_path = %q, want machine default", got)
	}
	wantEnv := []string{"ZCODE_HTTP_PROXY=http://proxy.example:8080", "K=v"}
	if !reflect.DeepEqual(merged.ListOptions["env"], wantEnv) {
		t.Fatalf("env = %v, want %v", merged.ListOptions["env"], wantEnv)
	}
}

func TestMergeMachineDefaultsOpenCodeBaseURL(t *testing.T) {
	backends := domain.MachineBackends{
		OpenCode: &domain.MachineBackendSettings{BaseURL: "http://127.0.0.1:4097"},
	}
	spec := agentSpecForMerge("opencode")
	merged := MergeMachineDefaults(spec, backends)
	if got := merged.StringOptions["base_url"]; got != "" {
		t.Fatalf("machine settings must remain private to runtime, got %q", got)
	}
	if len(merged.ListOptions["env"]) != 0 {
		t.Fatalf("opencode must get no env entries, got %v", merged.ListOptions["env"])
	}
}

func TestMergeMachineDefaultsUnknownBackendUnchanged(t *testing.T) {
	spec := agentSpecForMerge("fake")
	merged := MergeMachineDefaults(spec, domain.MachineBackends{Codex: &domain.MachineBackendSettings{Command: "/opt/codex"}})
	if !reflect.DeepEqual(merged, spec) {
		t.Fatalf("fake backend spec changed: %+v", merged)
	}
}

func hasDuplicateKeys(entries []string) bool {
	seen := map[string]bool{}
	for _, entry := range entries {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

func TestMergeMachineDefaultsZCodeExplicitEnvWins(t *testing.T) {
	backends := domain.MachineBackends{
		ZCode: &domain.MachineBackendSettings{ProxyURL: "http://proxy.example:8080"},
	}
	spec := agentSpecForMerge("zcode")
	spec.ListOptions["env"] = []string{"ZCODE_HTTP_PROXY=http://direct.example:3128"}
	merged := MergeMachineDefaults(spec, backends)
	wantEnv := []string{"ZCODE_HTTP_PROXY=http://direct.example:3128"}
	if !reflect.DeepEqual(merged.ListOptions["env"], wantEnv) {
		t.Fatalf("env = %v, want %v (explicit ZCODE_HTTP_PROXY wins, no duplicates)", merged.ListOptions["env"], wantEnv)
	}
	if hasDuplicateKeys(merged.ListOptions["env"]) {
		t.Fatalf("env has duplicate keys: %v", merged.ListOptions["env"])
	}
}

func TestLoadMachineBackendsInheritEnv(t *testing.T) {
	home := writeMachineBackends(t, `
codex:
  inherit_env:
    - HOME
    - PATH
kimi:
  inherit_env: HOME,PATH
zcode:
  inherit_env:
    - HOME
`)
	backends, err := LoadMachineBackends(home)
	if err != nil {
		t.Fatalf("LoadMachineBackends() error = %v", err)
	}
	if want := []string{"HOME", "PATH"}; !reflect.DeepEqual(backends.Codex.InheritEnv, want) {
		t.Errorf("codex inherit_env = %v, want %v", backends.Codex.InheritEnv, want)
	}
	if want := []string{"HOME", "PATH"}; !reflect.DeepEqual(backends.Kimi.InheritEnv, want) {
		t.Errorf("kimi inherit_env = %v, want %v (comma string)", backends.Kimi.InheritEnv, want)
	}
	if want := []string{"HOME"}; !reflect.DeepEqual(backends.ZCode.InheritEnv, want) {
		t.Errorf("zcode inherit_env = %v, want %v", backends.ZCode.InheritEnv, want)
	}
}

func TestLoadMachineBackendsInheritEnvRejectedWithoutChildProcess(t *testing.T) {
	for _, section := range []string{"judge"} {
		home := writeMachineBackends(t, section+":\n  inherit_env:\n    - HOME\n")
		_, err := LoadMachineBackends(home)
		if err == nil || !strings.Contains(err.Error(), "inherit_env") || !strings.Contains(err.Error(), section) {
			t.Fatalf("%s: expected unsupported inherit_env error, got %v", section, err)
		}
	}
}

func TestLoadMachineBackendsInheritEnvEmptyEntriesRejected(t *testing.T) {
	home := writeMachineBackends(t, "codex:\n  inherit_env: \"HOME,,PATH\"\n")
	if _, err := LoadMachineBackends(home); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-entry error, got %v", err)
	}
	home = writeMachineBackends(t, "codex:\n  inherit_env:\n    - HOME\n    - \"\"\n")
	if _, err := LoadMachineBackends(home); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-entry error for list, got %v", err)
	}
}

func TestMergeMachineDefaultsInheritEnvUnionsWithAgentList(t *testing.T) {
	backends := domain.MachineBackends{
		Codex: &domain.MachineBackendSettings{
			ProxyURL:   "http://proxy.example:8080",
			InheritEnv: []string{"HOME", "PATH"},
		},
	}
	spec := agentSpecForMerge("codex")
	spec.ListOptions["inherit_env"] = []string{"PATH", "CODEX_HOME"}
	merged := MergeMachineDefaults(spec, backends)
	wantInherit := []string{"HOME", "PATH", "CODEX_HOME"}
	if !reflect.DeepEqual(merged.ListOptions["inherit_env"], wantInherit) {
		t.Fatalf("inherit_env = %v, want %v", merged.ListOptions["inherit_env"], wantInherit)
	}
	// Suppression uses the union: no variable is both inherited and injected.
	for _, entry := range merged.ListOptions["env"] {
		if key, _, ok := strings.Cut(entry, "="); ok {
			for _, name := range wantInherit {
				if key == name {
					t.Fatalf("env injects inherited variable %q: %v", key, merged.ListOptions["env"])
				}
			}
		}
	}
	if hasDuplicateKeys(merged.ListOptions["env"]) {
		t.Fatalf("env has duplicate keys: %v", merged.ListOptions["env"])
	}
}

func TestMergeMachineDefaultsInheritEnvAloneApplies(t *testing.T) {
	backends := domain.MachineBackends{
		Kimi: &domain.MachineBackendSettings{InheritEnv: []string{"HOME", "PATH"}},
	}
	spec := agentSpecForMerge("kimi")
	merged := MergeMachineDefaults(spec, backends)
	if want := []string{"HOME", "PATH"}; !reflect.DeepEqual(merged.ListOptions["inherit_env"], want) {
		t.Fatalf("inherit_env = %v, want %v", merged.ListOptions["inherit_env"], want)
	}
	if len(merged.ListOptions["env"]) != 0 {
		t.Fatalf("inherit-only settings must add no env entries, got %v", merged.ListOptions["env"])
	}
	// Kimi's childEnv keys off inherit_env alone; that behavior is pinned in
	// the kimi package tests. Here: the input spec stays untouched.
	if len(spec.ListOptions["inherit_env"]) != 0 {
		t.Fatalf("input spec inherit_env mutated: %v", spec.ListOptions["inherit_env"])
	}
}

func TestMergeMachineDefaultsAgentEnvBeatsInheritedVariable(t *testing.T) {
	backends := domain.MachineBackends{
		Codex: &domain.MachineBackendSettings{InheritEnv: []string{"HOME"}},
	}
	spec := agentSpecForMerge("codex")
	spec.ListOptions["env"] = []string{"HOME=/other"}
	merged := MergeMachineDefaults(spec, backends)
	if !reflect.DeepEqual(merged.ListOptions["inherit_env"], []string{"HOME"}) {
		t.Fatalf("inherit_env = %v, want [HOME]", merged.ListOptions["inherit_env"])
	}
	// BuildEnv (cursor package) appends explicit entries after inherited
	// copies, so the built child environment carries HOME=/other; the merge
	// must leave both lists exactly as the agent and machine declared them.
	if !reflect.DeepEqual(merged.ListOptions["env"], []string{"HOME=/other"}) {
		t.Fatalf("env = %v, want [HOME=/other]", merged.ListOptions["env"])
	}
}

func TestLoadMachineBackendsValidationErrorOmitsValue(t *testing.T) {
	home := writeMachineBackends(t, "codex:\n  proxy_url: \"ftp://user:secret@proxy.example:3128\"\n")
	_, err := LoadMachineBackends(home)
	if err == nil {
		t.Fatal("expected invalid proxy_url error")
	}
	for _, leak := range []string{"secret", "user:secret", "proxy.example:3128", "ftp://"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error %q leaks value part %q", err, leak)
		}
	}
	for _, want := range []string{"proxy_url", "codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestLoadMachineBackendsRejectsNonStringScalars(t *testing.T) {
	cases := map[string]string{
		"bool command":     "codex:\n  command: false\n",
		"int command":      "codex:\n  command: 123\n",
		"null command":     "codex:\n  command: null\n",
		"int no_proxy":     "codex:\n  no_proxy: 1\n",
		"int inherit":      "codex:\n  inherit_env: 777\n",
		"seq int inherit":  "codex:\n  inherit_env:\n    - 123\n    - true\n",
		"seq bool inherit": "codex:\n  inherit_env:\n    - HOME\n    - true\n",
		"bool ca":          "cursor:\n  ca_cert_file: true\n",
		"bool no_proxy":    "codex:\n  no_proxy: true\n",
		"int proxy":        "codex:\n  proxy_url: 8080\n",
	}
	for name, content := range cases {
		home := writeMachineBackends(t, content)
		if _, err := LoadMachineBackends(home); err == nil {
			t.Errorf("%s: expected type error, got nil", name)
		}
	}
}

func TestLoadMachineBackendsRejectsMultipleDocuments(t *testing.T) {
	home := writeMachineBackends(t, "codex:\n  command: /a\n---\nclaude:\n  command: /b\n")
	_, err := LoadMachineBackends(home)
	if err == nil || !strings.Contains(err.Error(), "single YAML document") {
		t.Fatalf("expected single-document error, got %v", err)
	}
}

func TestLoadMachineBackendsRejectsBlankValues(t *testing.T) {
	for _, content := range []string{
		"codex:\n  proxy_url: \"   \"\n",
		"codex:\n  command: \"  \"\n",
		"codex:\n  no_proxy: \"  \"\n",
		"codex:\n  inherit_env: \"  \"\n",
	} {
		home := writeMachineBackends(t, content)
		if _, err := LoadMachineBackends(home); err == nil || !strings.Contains(err.Error(), "blank") {
			t.Fatalf("expected blank-value error for %q, got %v", content, err)
		}
	}
	// An explicit empty string stays a valid "unset".
	home := writeMachineBackends(t, "codex:\n  proxy_url: \"\"\n  command: /opt/codex\n")
	backends, err := LoadMachineBackends(home)
	if err != nil {
		t.Fatalf("explicit empty string must be valid: %v", err)
	}
	if backends.Codex.ProxyURL != "" || backends.Codex.Command != "/opt/codex" {
		t.Fatalf("unexpected settings: %+v", backends.Codex)
	}
}

func TestLoadMachineBackendsValidatesBaseURL(t *testing.T) {
	home := writeMachineBackends(t, "opencode:\n  mode: external\n  base_url: \"not a url\"\n")
	if _, err := LoadMachineBackends(home); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("expected base_url URL error, got %v", err)
	}
	home = writeMachineBackends(t, "opencode:\n  mode: external\n  base_url: \"http://127.0.0.1:4096\"\n")
	if _, err := LoadMachineBackends(home); err != nil {
		t.Fatalf("valid base_url rejected: %v", err)
	}
}
