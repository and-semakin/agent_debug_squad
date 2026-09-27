package codex

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
	"io"
)

func (a *Adapter) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("codex", "codex_app_server", "model/list", "account_catalog", in)
	r.Sources[0].HiddenPolicy.Applied = r.Sources[0].HiddenPolicy.Requested
	env := preflight.MaterializeEnv(BuildEnv(a.spec, in.AmbientEnv), in.AmbientEnv)
	command := a.spec.StringOptions["command"]
	if command == "" {
		command = "codex"
	}
	p, err := modelprobe.Start(ctx, command, []string{"app-server"}, env, in.WorkspaceDir)
	if err != nil {
		modelprobe.Fail(&r, modelprobe.ContextStatus(ctx, "error"), "app_server_start_failed")
		return r
	}
	defer p.Close()
	send := func(id int, method string, params any) error {
		return json.NewEncoder(p.In).Encode(map[string]any{"id": id, "method": method, "params": params})
	}
	receive := func(id int) (json.RawMessage, error) {
		for {
			b, e := p.Read()
			if e != nil {
				return nil, e
			}
			var msg struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(b, &msg) != nil {
				return nil, errors.New("invalid_frame")
			}
			if msg.ID == nil || *msg.ID != id {
				continue
			}
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				var failure struct {
					Code int `json:"code"`
					Data struct {
						Code string `json:"code"`
					} `json:"data"`
				}
				_ = json.Unmarshal(msg.Error, &failure)
				if failure.Code == 401 || failure.Data.Code == "authentication_required" {
					return nil, &modelprobe.CommandFailure{Status: "auth_required"}
				}
				return nil, errors.New("rpc_failed")
			}
			return msg.Result, nil
		}
	}
	fail := func(e error) {
		status := "unsupported"
		code := "model_protocol_failed"
		if errors.Is(e, modelprobe.ErrLimit) {
			status = "error"
			code = "source_limit"
		}
		var commandFailure *modelprobe.CommandFailure
		if errors.As(e, &commandFailure) {
			status = commandFailure.Status
		}
		if errors.Is(e, io.EOF) {
			status = "error"
		}
		modelprobe.Fail(&r, modelprobe.ContextStatus(ctx, status), code)
	}
	if err = send(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "agent_debug_squad_models", "version": "1"}}); err != nil {
		fail(err)
		return r
	}
	if _, err = receive(1); err != nil {
		fail(err)
		return r
	}
	if err = json.NewEncoder(p.In).Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		fail(err)
		return r
	}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < modelprobe.MaxPages; page++ {
		params := map[string]any{"limit": 100, "includeHidden": in.IncludeHidden}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err = send(page+2, "model/list", params); err != nil {
			fail(err)
			break
		}
		data, e := receive(page + 2)
		if e != nil {
			fail(e)
			break
		}
		var response struct {
			Data       *[]json.RawMessage `json:"data"`
			NextCursor *string            `json:"nextCursor"`
		}
		if json.Unmarshal(data, &response) != nil || response.Data == nil {
			fail(errors.New("shape"))
			break
		}
		modelprobe.Freshness(&r, 0, data)
		for _, raw := range *response.Data {
			if len(r.Models) >= modelprobe.MaxRows {
				modelprobe.Fail(&r, "partial", "row_limit")
				break
			}
			var m struct {
				ID            string `json:"id"`
				Model         string `json:"model"`
				DisplayName   string `json:"displayName"`
				Hidden        *bool  `json:"hidden"`
				Default       *bool  `json:"isDefault"`
				DefaultEffort string `json:"defaultReasoningEffort"`
				Efforts       []struct {
					Effort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
				Modalities []string `json:"inputModalities"`
			}
			if json.Unmarshal(raw, &m) != nil || m.Model == "" {
				modelprobe.Fail(&r, "error", "malformed_model_row")
				continue
			}
			x := modelprobe.Model("codex", m.Model, "model/list")
			x.CatalogID = m.ID
			x.DisplayName = m.DisplayName
			x.Hidden = m.Hidden
			x.IsDefault = m.Default
			x.InputModalities = m.Modalities
			if len(m.Efforts) > 0 || m.DefaultEffort != "" {
				par := domain.ModelParameter{NativeName: "reasoning_effort", Default: m.DefaultEffort, SquadOption: "reasoning"}
				for _, e := range m.Efforts {
					par.Values = append(par.Values, e.Effort)
				}
				x.Parameters = []domain.ModelParameter{par}
			}
			r.Models = append(r.Models, x)
		}
		if !r.Complete && len(r.Models) >= modelprobe.MaxRows {
			break
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			break
		}
		cursor = *response.NextCursor
		if seen[cursor] {
			modelprobe.Fail(&r, "partial", "repeated_cursor")
			break
		}
		seen[cursor] = true
		if page == modelprobe.MaxPages-1 {
			modelprobe.Fail(&r, "partial", "page_limit")
		}
	}
	modelprobe.Sanitize(&r, modelprobe.EnvSecrets(env))
	modelprobe.Finish(&r)
	return r
}
