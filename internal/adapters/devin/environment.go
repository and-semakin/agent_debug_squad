package devin

import (
	"context"
	"sort"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
)

// BuildEnv deliberately never returns nil (exec interprets nil as ambient).
func BuildEnv(spec domain.AgentSpec, ambient []string) []string {
	source := map[string]string{}
	for _, e := range ambient {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			source[k] = v
		}
	}
	values := map[string]string{}
	keys := []string{"HOME", "PATH", "TMPDIR", "TMP", "TEMP", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"}
	keys = append(keys, spec.ListOptions["inherit_env"]...)
	for _, k := range keys {
		if v, ok := source[k]; ok {
			values[k] = v
		}
	}
	for _, e := range spec.ListOptions["env"] {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			values[k] = v
		}
	}
	out := make([]string, 0, len(values))
	for k, v := range values {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
func (a *Adapter) CheckInstallation(ctx context.Context, in domain.InstallationInput) domain.InstallationResult {
	return preflight.CheckCLI(a.spec, in, "devin", "devin", BuildEnv)
}
func envValue(env []string, key string) string {
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}
