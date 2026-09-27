// Package modeldiscovery resolves and aggregates session-free backend catalogs.
package modeldiscovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/adapters/codex"
	"github.com/and-semakin/agent_debug_squad/internal/config"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"github.com/and-semakin/agent_debug_squad/internal/preflight"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

var RealBackends = []string{"codex", "cursor", "kimi", "opencode", "zcode"}

type Options struct {
	All                   bool
	Backends              []string
	ConfigPath, Workspace string
	IncludeHidden         bool
}
type Target struct {
	ID     string
	Agents []string
	Spec   domain.AgentSpec
	Input  domain.ModelDiscoveryInput
}

func Resolve(o Options, machine domain.MachineBackends, ambient []string) ([]Target, error) {
	if o.All && len(o.Backends) > 0 {
		return nil, errors.New("invalid_arguments")
	}
	selected := map[string]bool{}
	for _, b := range o.Backends {
		switch b {
		case "codex", "cursor", "kimi", "opencode", "zcode", "fake":
			selected[b] = true
		default:
			return nil, errors.New("invalid_arguments")
		}
	}
	var specs []domain.AgentSpec
	workspace := o.Workspace
	if o.ConfigPath != "" {
		cfg, e := config.Load(o.ConfigPath)
		if e != nil {
			return nil, errors.New("invalid_configuration")
		}
		if workspace != "" {
			w, e := filepath.Abs(workspace)
			if e != nil || w != cfg.WorkspaceDir {
				return nil, errors.New("invalid_configuration")
			}
		}
		workspace = cfg.WorkspaceDir
		specs = cfg.Agents
	}
	if workspace == "" {
		workspace = "."
	}
	workspace, e := filepath.Abs(workspace)
	if e != nil {
		return nil, errors.New("invalid_configuration")
	}
	if o.All || o.ConfigPath == "" && len(selected) == 0 {
		for _, b := range RealBackends {
			selected[b] = true
		}
	}
	targets := []Target{}
	represented := map[string]bool{}
	for _, spec := range specs {
		if len(selected) > 0 && !selected[spec.Backend] && !(o.All && spec.Backend == "fake") {
			continue
		}
		targets = append(targets, Target{Agents: []string{spec.Name}, Spec: spec})
		represented[spec.Backend] = true
	}
	for b := range selected {
		if !represented[b] {
			targets = append(targets, Target{Agents: []string{}, Spec: domain.AgentSpec{Backend: b, StringOptions: map[string]string{}, ListOptions: map[string][]string{}}})
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("no_targets")
	}
	groups := map[string]int{}
	out := []Target{}
	for _, t := range targets {
		switch t.Spec.Backend {
		case "codex", "cursor", "kimi", "opencode", "zcode", "fake":
		default:
			return nil, errors.New("invalid_configuration")
		}
		t.Spec = config.MergeMachineDefaults(t.Spec, machine)
		t.Input = domain.ModelDiscoveryInput{WorkspaceDir: workspace, AmbientEnv: append([]string{}, ambient...), IncludeHidden: o.IncludeHidden}
		// Private equality key includes effective environment, settings and launch-affecting options.
		opts := map[string]string{}
		for k, v := range t.Spec.StringOptions {
			switch k {
			case "model", "reasoning", "yolo":
				continue
			}
			opts[k] = v
		}
		env := preflight.MaterializeEnv(codex.BuildEnv(t.Spec, ambient), ambient)
		if t.Spec.Backend == "kimi" && len(t.Spec.ListOptions["env"]) == 0 && len(t.Spec.ListOptions["inherit_env"]) == 0 {
			env = ambient
		}
		envMap := map[string]string{}
		for _, e := range env {
			k, v, ok := strings.Cut(e, "=")
			if ok {
				envMap[k] = v
			}
		}
		keyData := []any{t.Spec.Backend, workspace, opts, envMap}
		if t.Spec.Backend == "opencode" {
			keyData = append(keyData, machine.OpenCode, ambient)
		}
		b, _ := json.Marshal(keyData)
		key := string(b)
		if i, ok := groups[key]; ok {
			out[i].Agents = append(out[i].Agents, t.Agents...)
			continue
		}
		groups[key] = len(out)
		out = append(out, t)
	}
	for i := range out {
		t := &out[i]
		sort.Strings(t.Agents)
		if len(t.Agents) == 0 {
			t.ID = "machine:" + t.Spec.Backend
		} else {
			names := []string{}
			for _, n := range t.Agents {
				names = append(names, strings.ReplaceAll(url.QueryEscape(n), "+", "%20"))
			}
			t.ID = fmt.Sprintf("agents:%s:%s", t.Spec.Backend, strings.Join(names, ","))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spec.Backend == out[j].Spec.Backend {
			return out[i].ID < out[j].ID
		}
		return out[i].Spec.Backend < out[j].Spec.Backend
	})
	return out, nil
}
