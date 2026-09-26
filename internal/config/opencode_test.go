package config

import (
	"strings"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestOpenCodeMachineContract(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"managed", "mode: managed\n  command: opencode\n  proxy_url: http://alice:secret@proxy.test:3128\n  no_proxy: [example.test, '::1']\n  snapshot: false\n  inherit_env: [API_KEY]", ""},
		{"default", "snapshot: true", ""},
		{"external", "mode: external\n  base_url: http://localhost:4096\n  snapshot: false", ""},
		{"legacy", "base_url: http://localhost:4096", "mode: external"},
		{"bad mode", "mode: nope", "mode"},
		{"bad snapshot", "snapshot: 'false'", "boolean"},
		{"external no endpoint", "mode: external", "base_url"},
		{"external command", "mode: external\n  base_url: http://localhost:4096\n  command: ''", "command"},
		{"external proxy", "mode: external\n  base_url: http://localhost:4096\n  proxy_url: http://alice:secret@proxy.test", "proxy_url"},
		{"external exclusions", "mode: external\n  base_url: http://localhost:4096\n  no_proxy: []", "no_proxy"},
		{"external inheritance", "mode: external\n  base_url: http://localhost:4096\n  inherit_env: []", "inherit_env"},
		{"invalid private proxy", "proxy_url: alice:secret", "proxy_url"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadMachineBackends(writeMachineBackends(t, "opencode:\n  "+tt.body+"\n"))
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if cfg.OpenCode == nil {
					t.Fatal("missing settings")
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v want %s", err, tt.want)
			}
			if err != nil && (strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), "secret")) {
				t.Fatal("credential leak")
			}
		})
	}
}

func TestOpenCodeProcessOverridesRejected(t *testing.T) {
	for _, key := range []string{"command", "proxy_url", "no_proxy", "snapshot", "env", "inherit_env"} {
		spec := domain.AgentSpec{Backend: "opencode", Options: map[string]any{key: ""}}
		if err := ValidateOpenCodeAgent(spec); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s: %v", key, err)
		}
	}
}
