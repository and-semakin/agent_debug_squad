package kimi

import (
	"context"
	"encoding/json"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
	"sort"
)

func (a *Adapter) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	env := preflight.MaterializeEnv(childEnv(a.spec, in.AmbientEnv), in.AmbientEnv)
	command := a.spec.StringOptions["command"]
	if command == "" {
		command = "kimi"
	}
	b, e := modelprobe.Collect(ctx, command, []string{"provider", "list", "--json"}, env, in.WorkspaceDir)
	r := parseModels(b, in)
	if e != nil {
		modelprobe.Fail(&r, modelprobe.CommandStatus(ctx, e), "list_command_failed")
	}
	modelprobe.Sanitize(&r, modelprobe.EnvSecrets(env))
	modelprobe.Finish(&r)
	return r
}
func parseModels(b []byte, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("kimi", "kimi_local_config", "provider list --json", "configured", in)
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil || raw == nil {
		modelprobe.Fail(&r, "unsupported", "unrecognized_model_list")
		return r
	}
	var data struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	if json.Unmarshal(b, &data) != nil || data.Models == nil {
		modelprobe.Fail(&r, "unsupported", "unrecognized_model_list")
		return r
	}
	modelprobe.Freshness(&r, 0, b)
	aliases := []string{}
	for alias := range data.Models {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if len(r.Models) >= modelprobe.MaxRows {
			modelprobe.Fail(&r, "partial", "row_limit")
			break
		}
		var x struct {
			Provider       string   `json:"provider"`
			Model          string   `json:"model"`
			DisplayName    string   `json:"displayName"`
			MaxContextSize *int64   `json:"maxContextSize"`
			Capabilities   []string `json:"capabilities"`
			SupportEfforts []string `json:"supportEfforts"`
			DefaultEffort  string   `json:"defaultEffort"`
		}
		if json.Unmarshal(data.Models[alias], &x) != nil || x.Provider == "" || x.Model == "" {
			modelprobe.Fail(&r, "error", "malformed_model_row")
			continue
		}
		m := modelprobe.Model("kimi", x.Model, "provider list --json")
		m.ProviderID = x.Provider
		m.Alias = alias
		m.Configured = modelprobe.Bool(true)
		m.DisplayName = x.DisplayName
		m.ContextWindow = x.MaxContextSize
		m.Selection.Options["model"] = alias
		if len(x.SupportEfforts) > 0 || x.DefaultEffort != "" {
			m.Parameters = append(m.Parameters, domain.ModelParameter{NativeName: "effort", Values: x.SupportEfforts, Default: x.DefaultEffort})
		}
		if len(x.Capabilities) > 0 {
			m.Parameters = append(m.Parameters, domain.ModelParameter{NativeName: "capabilities", Values: x.Capabilities})
		}
		r.Models = append(r.Models, m)
	}
	modelprobe.Sanitize(&r, modelprobe.Secrets(raw))
	return r
}
