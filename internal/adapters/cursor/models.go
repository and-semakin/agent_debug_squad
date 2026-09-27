package cursor

import (
	"context"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/modelprobe"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
	"regexp"
	"strings"
)

var ansiModels = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func (a *Adapter) ListModels(ctx context.Context, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	env := preflight.MaterializeEnv(BuildEnv(a.spec, in.AmbientEnv), in.AmbientEnv)
	command := a.spec.StringOptions["command"]
	if command == "" {
		command = "cursor-agent"
	}
	b, e := modelprobe.Collect(ctx, command, []string{"--list-models"}, env, in.WorkspaceDir)
	r := parseModels(b, in)
	if e != nil {
		modelprobe.Fail(&r, modelprobe.CommandStatus(ctx, e), "list_command_failed")
	}
	modelprobe.Sanitize(&r, modelprobe.EnvSecrets(env))
	modelprobe.Finish(&r)
	return r
}
func parseModels(b []byte, in domain.ModelDiscoveryInput) domain.ModelDiscoveryResult {
	r := modelprobe.New("cursor", "cursor_cli_text", "--list-models", "account_catalog", in)
	text := strings.TrimSpace(ansiModels.ReplaceAllString(string(b), ""))
	lines := strings.Split(text, "\n")
	if text == "No models available" || text == "Available models\n\nNo models available" {
		return r
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "Available models" {
		modelprobe.Fail(&r, "unsupported", "unrecognized_model_list")
		return r
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Tip: use --model <id> (or /model <id> in interactive mode) to switch.") {
			continue
		}
		id, name, ok := strings.Cut(line, " - ")
		if !ok || id == "" || name == "" {
			modelprobe.Fail(&r, "unsupported", "malformed_model_row")
			continue
		}
		if len(r.Models) >= modelprobe.MaxRows {
			modelprobe.Fail(&r, "partial", "row_limit")
			break
		}
		m := modelprobe.Model("cursor", id, "--list-models")
		m.DisplayName = name
		if strings.HasSuffix(name, " (default)") {
			m.DisplayName = strings.TrimSuffix(name, " (default)")
			m.IsDefault = modelprobe.Bool(true)
		}
		r.Models = append(r.Models, m)
	}
	if len(r.Models) == 0 {
		modelprobe.Fail(&r, "unsupported", "unrecognized_empty_list")
	}
	return r
}
