package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"io"
	"net"
	"net/http"
	"sort"
)

func (a *Adapter) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("opencode", "opencode_provider_api", "/provider", "known_catalog", in)
	if issue := a.ReadinessCheck(ctx); issue != nil {
		modelprobe.Issue(&r, ctx, *issue)
		return r
	}
	if err := a.checkReady(ctx); err != nil {
		modelprobe.Fail(&r, modelprobe.ContextStatus(ctx, "error"), "snapshot_verification_failed")
		return r
	}
	second := modelprobe.New("opencode", "opencode_provider_api", "/config/providers", "configured", in).Sources[0]
	r.Sources = append(r.Sources, second)
	var all struct {
		All       *[]json.RawMessage `json:"all"`
		Connected *[]string          `json:"connected"`
		Default   map[string]string  `json:"default"`
	}
	var configured struct {
		Providers *[]json.RawMessage `json:"providers"`
		Default   map[string]string  `json:"default"`
	}
	data, status, e := a.modelJSON(ctx, "/provider")
	modelprobe.Freshness(&r, 0, data)
	allOK := e == nil && json.Unmarshal(data, &all) == nil && all.All != nil && all.Connected != nil
	secrets := []string{a.spec.StringOptions["password"]}
	var raw any
	_ = json.Unmarshal(data, &raw)
	secrets = append(secrets, modelprobe.Secrets(raw)...)
	if !allOK {
		if e == nil {
			status = "unsupported"
		}
		modelprobe.Fail(&r, status, "provider_list_failed")
	}
	data, status, e = a.modelJSON(ctx, "/config/providers")
	modelprobe.Freshness(&r, 1, data)
	configOK := e == nil && json.Unmarshal(data, &configured) == nil && configured.Providers != nil
	raw = nil
	_ = json.Unmarshal(data, &raw)
	secrets = append(secrets, modelprobe.Secrets(raw)...)
	if !configOK {
		if e == nil {
			status = "unsupported"
		}
		modelprobe.Fail(&r, status, "configured_provider_list_failed")
	}
	connected := map[string]bool{}
	if allOK {
		for _, id := range *all.Connected {
			connected[id] = true
		}
	}
	configuredIDs := map[string]bool{}
	rows := map[string][]domain.CatalogModel{}
	parse := func(providers []json.RawMessage, source string, defaults map[string]string) bool {
		complete := true
		for _, raw := range providers {
			var p struct {
				ID     string                     `json:"id"`
				Name   string                     `json:"name"`
				Models map[string]json.RawMessage `json:"models"`
			}
			if json.Unmarshal(raw, &p) != nil || p.ID == "" || p.Models == nil {
				complete = false
				modelprobe.Fail(&r, "unsupported", "malformed_provider")
				continue
			}
			ids := []string{}
			for id := range p.Models {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				key := p.ID + "\x00" + id
				if source == "/config/providers" {
					configuredIDs[key] = true
				}
				if len(rows) >= modelprobe.MaxRows && len(rows[key]) == 0 {
					complete = false
					modelprobe.Fail(&r, "partial", "row_limit")
					continue
				}
				var x struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Limit struct {
						Context *int64 `json:"context"`
					} `json:"limit"`
					Modalities struct {
						Input []string `json:"input"`
					} `json:"modalities"`
					Variants map[string]json.RawMessage `json:"variants"`
				}
				if json.Unmarshal(p.Models[id], &x) != nil || x.ID != "" && x.ID != id {
					complete = false
					modelprobe.Fail(&r, "unsupported", "malformed_model_row")
					continue
				}
				m := modelprobe.Model("opencode", id, source)
				m.ProviderID = p.ID
				m.ProviderLabel = p.Name
				m.DisplayName = x.Name
				m.ContextWindow = x.Limit.Context
				m.InputModalities = x.Modalities.Input
				m.Selection.Options["model"] = p.ID + "/" + id
				if d, ok := defaults[p.ID]; ok {
					m.IsDefault = modelprobe.Bool(d == id)
				}
				variants := []string{}
				for v := range x.Variants {
					variants = append(variants, v)
				}
				sort.Strings(variants)
				if len(variants) > 0 {
					m.Parameters = []domain.ModelParameter{{NativeName: "variant", Values: variants}}
				}
				rows[key] = append(rows[key], m)
			}
		}
		return complete
	}
	// Keep both observations: conflicting metadata is reported, never silently preferred.
	if configOK {
		configOK = parse(*configured.Providers, "/config/providers", configured.Default)
	}
	if allOK {
		allOK = parse(*all.All, "/provider", all.Default)
	}
	for key, observations := range rows {
		for _, m := range observations {
			if configOK {
				m.Configured = modelprobe.Bool(configuredIDs[key])
			}
			if allOK {
				m.Connected = modelprobe.Bool(connected[m.ProviderID])
			}
			r.Models = append(r.Models, m)
		}
	}
	r.Sources[0].Complete = allOK
	r.Sources[1].Complete = configOK
	if a.runtime != nil {
		secrets = append(secrets, modelprobe.EnvSecrets(a.runtime.ambient)...)
		secrets = append(secrets, a.runtime.settings.ProxyURL)
	}
	modelprobe.Sanitize(&r, secrets)
	modelprobe.Finish(&r)
	return r
}
func (a *Adapter) modelJSON(ctx context.Context, path string) ([]byte, string, error) {
	req, err := a.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, "error", err
	}
	resp, err := a.httpClient().Do(req)
	if err != nil {
		status := modelprobe.ContextStatus(ctx, "error")
		var ne net.Error
		if ctx.Err() == nil && errors.As(err, &ne) {
			status = "offline"
		}
		return nil, status, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := "error"
		if resp.StatusCode == 401 {
			status = "auth_required"
		}
		if resp.StatusCode == 404 {
			status = "unsupported"
		}
		return nil, status, errors.New("http_failure")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, modelprobe.MaxFrame+1))
	if len(b) > modelprobe.MaxFrame {
		return nil, "error", modelprobe.ErrLimit
	}
	return b, modelprobe.ContextStatus(ctx, "error"), e
}
