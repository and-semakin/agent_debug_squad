package config

import (
	"reflect"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/adapters/devin"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestDevinOptionsValidation(t *testing.T) {
	for _, options := range []map[string]any{{"reasoning": ""}, {"speed": "turbo"}, {"mode": "invalid"}, {"mode": "ask", "yolo": true}, {"mode": "bypass", "yolo": false}, {"sandbox": true}, {"model": 3}, {"model": []string{"x"}}, {"env": "X=y"}, {"yolo": "maybe"}, {"yolo": "false"}, {"env": []string{"DEVIN_REFUSAL_FALLBACK=true"}}} {
		if _, ok := options["model"]; !ok {
			options["model"] = "swe-2-high"
		}
		_, e := NormalizeAgentOptions(domain.AgentSpec{Name: "test", StartupPrompt: "test", Backend: "devin", Options: options})
		if e == nil {
			t.Fatal("accepted unsupported options", options)
		}
	}
	s, e := NormalizeAgentOptions(domain.AgentSpec{Name: "test", StartupPrompt: "test", Backend: "devin", Options: map[string]any{"model": "swe-2-high", "yolo": false, "inherit_env": []string{"HTTPS_PROXY"}}})
	if e != nil || s.Yolo == nil || *s.Yolo {
		t.Fatal(s, e)
	}
}
func TestDevinMachineDefaults(t *testing.T) {
	home := writeMachineBackends(t, "devin:\n  command: /opt/devin\n  proxy_url: http://proxy.example:8080\n  no_proxy: localhost\n  inherit_env: [EXTRA]\n")
	m, e := LoadMachineBackends(home)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(m.Configured(), []string{"devin"}) {
		t.Fatal(m.Configured())
	}
	s := MergeMachineDefaults(domain.AgentSpec{Backend: "devin", StringOptions: map[string]string{}, ListOptions: map[string][]string{"env": {"HTTPS_PROXY=http://override"}}}, m)
	if s.StringOptions["command"] != "/opt/devin" {
		t.Fatal(s)
	}
	got := devin.BuildEnv(s, []string{"EXTRA=kept", "HTTP_PROXY=not-implicit"})
	want := []string{"EXTRA=kept", "HTTPS_PROXY=http://override", "HTTP_PROXY=http://proxy.example:8080", "NO_PROXY=localhost"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestDevinExampleParses(t *testing.T) {
	cfg, e := Load("../../examples/devin-squad.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Backend != "devin" || cfg.Agents[0].Yolo == nil || !*cfg.Agents[0].Yolo {
		t.Fatal("example must explicitly select bypass")
	}
}

func TestDevinRequiredModelAndControlCombinations(t *testing.T) {
	for _, opts := range []map[string]any{nil, {"model": ""}, {"model": " "}} {
		if _, err := NormalizeAgentOptions(domain.AgentSpec{Name: "test", StartupPrompt: "test", Backend: "devin", Options: opts}); err == nil {
			t.Fatal("accepted missing model")
		}
	}
	for _, opts := range []map[string]any{
		{"model": "swe-2-high"},
		{"model": "swe-2-high", "mode": "ask"},
		{"model": "swe-2-high", "mode": "plan", "yolo": false},
		{"model": "swe-2-high", "mode": "bypass", "yolo": true, "reasoning": "medium", "speed": "standard"},
	} {
		if _, err := NormalizeAgentOptions(domain.AgentSpec{Name: "test", StartupPrompt: "test", Backend: "devin", Options: opts}); err != nil {
			t.Fatal(err)
		}
	}
}
