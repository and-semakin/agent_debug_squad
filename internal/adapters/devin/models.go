package devin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
)

func (a *Adapter) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("devin", "devin_models", "models list --format json", "account", in)
	env := BuildEnv(a.spec, in.AmbientEnv)
	workspace, e := canonical(in.WorkspaceDir)
	if e != nil {
		modelprobe.Fail(&r, "error", "workspace_unavailable")
		return r
	}
	path, cleanup, e := profile(env, workspace)
	if e != nil {
		modelprobe.Fail(&r, "unsupported", "read_only_catalog_unavailable")
		return r
	}
	defer cleanup()
	command := a.spec.StringOptions["command"]
	if command == "" {
		command = "devin"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, e := modelprobe.Collect(ctx, command, []string{"--config", path, "models", "list", "--format", "json"}, env, workspace)
	r = parseModels(b, in)
	if e != nil {
		modelprobe.Fail(&r, modelprobe.CommandStatus(ctx, e), "list_command_failed")
	}
	modelprobe.Sanitize(&r, modelprobe.EnvSecrets(env))
	modelprobe.Finish(&r)
	return r
}
func parseModels(b []byte, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("devin", "devin_models", "models list --format json", "account", in)
	var data struct {
		Families []struct {
			Variants []struct {
				ID      string `json:"model_uid"`
				Label   string `json:"label"`
				Context *int64 `json:"max_context_tokens"`
			} `json:"variants"`
		} `json:"families"`
	}
	if json.Unmarshal(b, &data) != nil || data.Families == nil {
		modelprobe.Fail(&r, "unsupported", "unrecognized_model_list")
		return r
	}
	seen := map[string]bool{}
	for _, f := range data.Families {
		if f.Variants == nil {
			modelprobe.Fail(&r, "partial", "malformed_model_family")
			continue
		}
		for _, v := range f.Variants {
			if len(r.Models) >= modelprobe.MaxRows {
				modelprobe.Fail(&r, "partial", "row_limit")
				return r
			}
			if v.ID == "" || seen[v.ID] || v.Context != nil && *v.Context <= 0 {
				modelprobe.Fail(&r, "partial", "malformed_model_row")
				continue
			}
			seen[v.ID] = true
			m := modelprobe.Model("devin", v.ID, "models list --format json")
			// The JSON catalog has variants, not the ACP model selector's subset.
			// Do not infer thought-level mappings from suffixes or start a
			// conversation to discover executable selections.
			m.Selection.Supported = false
			m.Selection.Options = map[string]string{}
			m.Selection.Reason = "requires_live_acp_model_option"
			m.DisplayName = v.Label
			m.ContextWindow = v.Context
			r.Models = append(r.Models, m)
		}
	}
	var raw any
	if json.Unmarshal(b, &raw) == nil {
		modelprobe.Sanitize(&r, modelprobe.Secrets(raw))
	}
	return r
}
