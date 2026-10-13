package devin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func control(options []configOption, key string) (*configOption, error) {
	var found *configOption
	for i := range options {
		o := &options[i]
		matches := o.Category == key
		if key == "speed" {
			matches = o.ID == "speed" && o.Category == "model_config"
		}
		if !matches {
			continue
		}
		if found != nil || o.ID == "" {
			return nil, fmt.Errorf("devin: ambiguous %s control", key)
		}
		found = o
	}
	return found, nil
}
func offered(o *configOption, value string) bool {
	if o == nil {
		return false
	}
	for _, v := range o.Options {
		if v.Value == value {
			return true
		}
	}
	return false
}

// Every setter returns the current complete options: selecting a model can
// replace its reasoning/speed choices. Never reuse the initial session schema.
func setControl(ctx context.Context, c *client, sid string, options []configOption, key, value string, handler func(message) error) ([]configOption, error) {
	o, err := control(options, key)
	if err != nil {
		return nil, err
	}
	if !offered(o, value) {
		return nil, fmt.Errorf("devin: requested %s is not offered by this ACP session", key)
	}
	b, err := boundedCall(ctx, c, "session/set_config_option", map[string]string{"sessionId": sid, "configId": o.ID, "value": value}, handler)
	if err != nil {
		return nil, err
	}
	var got session
	if json.Unmarshal(b, &got) != nil {
		return nil, fmt.Errorf("devin: invalid configuration response")
	}
	applied, err := control(got.Options, key)
	if err != nil {
		return nil, err
	}
	if applied == nil || applied.ID != o.ID || applied.Current != value || !offered(applied, value) {
		return nil, fmt.Errorf("devin: requested %s was not applied", key)
	}
	return got.Options, nil
}

func effectiveMode(spec domain.AgentSpec) string {
	if mode := spec.StringOptions["mode"]; mode != "" {
		return mode
	}
	yolo := true
	if raw, ok := spec.StringOptions["yolo"]; ok {
		yolo, _ = strconv.ParseBool(raw)
	} else if spec.Yolo != nil {
		yolo = *spec.Yolo
	}
	if !yolo {
		return "normal"
	}
	return "bypass"
}

func applyControls(ctx context.Context, c *client, sid string, options []configOption, spec domain.AgentSpec, handler func(message) error) error {
	expected := map[string]string{"model": spec.StringOptions["model"], "mode": effectiveMode(spec)}
	var err error
	options, err = setControl(ctx, c, sid, options, "model", expected["model"], handler)
	if err != nil {
		return err
	}
	reasoning, err := control(options, "thought_level")
	if err != nil {
		return err
	}
	value := spec.StringOptions["reasoning"]
	if value == "" && reasoning != nil {
		value = reasoning.Current
		if offered(reasoning, "high") {
			value = "high"
		}
		if !offered(reasoning, value) {
			return fmt.Errorf("devin: unverified default reasoning")
		}
	}
	if value != "" {
		options, err = setControl(ctx, c, sid, options, "thought_level", value, handler)
		if err != nil {
			return err
		}
		expected["thought_level"] = value
	}
	speed := spec.StringOptions["speed"]
	if speed == "" {
		speed = "standard"
	}
	speedOption, err := control(options, "speed")
	if err != nil {
		return err
	}
	if speedOption != nil || speed != "standard" {
		options, err = setControl(ctx, c, sid, options, "speed", speed, handler)
		if err != nil {
			return err
		}
		expected["speed"] = speed
	}
	options, err = setControl(ctx, c, sid, options, "mode", expected["mode"], handler)
	if err != nil {
		return err
	}
	// A later setter must not silently reset an earlier explicit choice.
	for _, key := range []string{"model", "thought_level", "speed", "mode"} {
		o, err := control(options, key)
		if err != nil {
			return err
		}
		want, present := expected[key]
		if !present {
			if o != nil {
				return fmt.Errorf("devin: %s control changed during configuration", key)
			}
		} else if o == nil || o.Current != want || !offered(o, want) {
			return fmt.Errorf("devin: %s changed during configuration", key)
		}
	}
	return nil
}
