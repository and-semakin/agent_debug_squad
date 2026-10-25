package opencode2

import (
	"context"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
)

type catalogModel struct {
	ID         string `json:"id"`
	ModelID    string `json:"modelID"`
	ProviderID string `json:"providerID"`
	Name       string `json:"name"`
	Enabled    *bool  `json:"enabled"`
	Limit      struct {
		Context *int64 `json:"context"`
	} `json:"limit"`
	Variants []struct {
		ID string `json:"id"`
	} `json:"variants"`
}

func (a *Adapter) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("opencode2", "opencode2_model_api", "/api/model", "configured", in)
	if issue := a.ReadinessCheck(ctx); issue != nil {
		modelprobe.Issue(&r, ctx, *issue)
		return r
	}
	if err := a.checkReady(ctx); err != nil {
		modelprobe.Fail(&r, modelprobe.ContextStatus(ctx, "error"), "configuration_verification_failed")
		return r
	}
	r.Sources = append(r.Sources, modelprobe.New("opencode2", "opencode2_provider_api", "/api/provider", "configured", in).Sources[0], modelprobe.New("opencode2", "opencode2_default_api", "/api/model/default", "configured", in).Sources[0])
	var info struct {
		Version string `json:"version"`
	}
	if a.doJSON(ctx, "GET", "/api/info", nil, &info) == nil {
		for i := range r.Sources {
			r.Sources[i].Version = info.Version
		}
	}
	var models struct {
		Data *[]catalogModel `json:"data"`
	}
	if err := a.doJSON(ctx, "GET", "/api/model", nil, &models); err != nil || models.Data == nil {
		modelprobe.Fail(&r, modelprobe.ContextStatus(ctx, "error"), "model_list_failed")
		return r
	}
	var providers struct {
		Data *[]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	providerOK := a.doJSON(ctx, "GET", "/api/provider", nil, &providers) == nil && providers.Data != nil
	names := map[string]string{}
	if providerOK {
		for _, p := range *providers.Data {
			names[p.ID] = p.Name
		}
	}
	var def struct {
		Data *catalogModel `json:"data"`
	}
	defaultOK := a.doJSON(ctx, "GET", "/api/model/default", nil, &def) == nil
	for _, x := range *models.Data {
		if x.ID == "" || x.ProviderID == "" {
			modelprobe.Fail(&r, "unsupported", "malformed_model_row")
			continue
		}
		if len(r.Models) >= modelprobe.MaxRows {
			modelprobe.Fail(&r, "partial", "row_limit")
			break
		}
		m := modelprobe.Model("opencode2", x.ModelID, "/api/model")
		if m.ModelID == "" {
			m.ModelID = x.ID
		}
		m.CatalogID = x.ID
		m.ProviderID = x.ProviderID
		m.ProviderLabel = names[x.ProviderID]
		if providerOK {
			m.Sources = append(m.Sources, "/api/provider")
		}
		if defaultOK {
			m.Sources = append(m.Sources, "/api/model/default")
		}
		m.DisplayName = x.Name
		m.ContextWindow = x.Limit.Context
		m.Selection.Options["model"] = x.ProviderID + "/" + x.ID
		if x.Enabled != nil && !*x.Enabled {
			m.DisabledReason = "disabled by backend"
		}
		if defaultOK && def.Data != nil {
			m.IsDefault = modelprobe.Bool(def.Data.ID == x.ID && def.Data.ProviderID == x.ProviderID)
		}
		var variants []string
		for _, v := range x.Variants {
			if v.ID != "" {
				variants = append(variants, v.ID)
			}
		}
		if len(variants) > 0 {
			m.Parameters = []domain.ModelParameter{{NativeName: "variant", Values: variants}}
		}
		r.Models = append(r.Models, m)
	}

	if !providerOK {
		modelprobe.Fail(&r, "partial", "provider_list_failed")
	}
	if !defaultOK {
		modelprobe.Fail(&r, "partial", "default_model_failed")
	}
	// Native endpoints expose a snapshot and no hidden filtering or connectivity proof.
	for i := range r.Sources {
		r.Sources[i].HiddenPolicy.Applied = "not_supported"
	}
	r.Sources[1].Complete = providerOK
	r.Sources[2].Complete = defaultOK
	modelprobe.Sanitize(&r, append(modelprobe.EnvSecrets(in.AmbientEnv), a.spec.StringOptions["password"]))
	modelprobe.Finish(&r)
	return r
}
