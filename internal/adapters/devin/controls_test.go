package devin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func TestModelSpecificControlsOnNewAndLoadedTurns(t *testing.T) {
	a, state := fixtureAdapter(t, "ok")
	a.spec.StringOptions["model"] = "controlled"
	state = initFixture(t, a, state)
	for i := 0; i < 2; i++ {
		_, next, err := a.Send(context.Background(), state, domain.RunRequest{RunID: "controls", Message: "hello"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		state = next
	}
	cs := calls(t, state.WorkspaceDir, "session/set_config_option")
	if len(cs) != 8 {
		t.Fatalf("expected four controls per turn: %d", len(cs))
	}
	want := []string{"model=controlled", "thought_level=high", "speed=standard", "mode=bypass"}
	for i, c := range cs {
		var p struct {
			ID    string `json:"configId"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(c.Params, &p); err != nil {
			t.Fatal(err)
		}
		if p.ID+"="+p.Value != want[i%4] {
			t.Fatalf("control %d: %s=%s", i, p.ID, p.Value)
		}
	}
}
func TestControlChoicesAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, scenario, model, reasoning, speed, mode string
		fail                                          bool
	}{
		{name: "explicit", model: "controlled", reasoning: "max", speed: "fast", mode: "ask"},
		{name: "native reasoning default", model: "no-high"},
		{name: "no optional selectors", model: "swe-2-high"},
		{name: "unsupported reasoning", model: "controlled", reasoning: "low", fail: true},
		{name: "missing reasoning", model: "swe-2-high", reasoning: "high", fail: true},
		{name: "missing speed", model: "swe-2-high", speed: "fast", fail: true},
		{name: "ambiguous", scenario: "ambiguous-control", model: "controlled", fail: true},
		{name: "later setter resets", scenario: "reset-control", model: "controlled", fail: true},
		{name: "ignored", scenario: "ignored-control", model: "controlled", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := tc.scenario
			if scenario == "" {
				scenario = "ok"
			}
			a, state := fixtureAdapter(t, scenario)
			a.spec.StringOptions["model"] = tc.model
			for k, v := range map[string]string{"reasoning": tc.reasoning, "speed": tc.speed, "mode": tc.mode} {
				if v != "" {
					a.spec.StringOptions[k] = v
				}
			}
			state = initFixture(t, a, state)
			_, _, err := a.Send(context.Background(), state, domain.RunRequest{RunID: "test", Message: "hello"}, nil)
			if (err != nil) != tc.fail {
				t.Fatalf("fail=%v err=%v", tc.fail, err)
			}
			if tc.fail && len(calls(t, state.WorkspaceDir, "session/prompt")) != 0 {
				t.Fatal("failure submitted prompt")
			}
		})
	}
}

func TestModeOverridesOnlyImplicitYolo(t *testing.T) {
	no := false
	for _, tc := range []struct {
		options map[string]string
		yolo    *bool
		want    string
	}{
		{map[string]string{}, nil, "bypass"},
		{map[string]string{}, &no, "normal"},
		{map[string]string{"mode": "ask"}, nil, "ask"},
		{map[string]string{"mode": "bypass"}, &no, "bypass"},
		{map[string]string{"yolo": "false"}, nil, "normal"},
	} {
		if got := effectiveMode(domain.AgentSpec{StringOptions: tc.options, Yolo: tc.yolo}); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
