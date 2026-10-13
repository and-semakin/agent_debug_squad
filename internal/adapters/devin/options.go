package devin

import (
	"fmt"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"strconv"
	"strings"
)

func ValidateSpec(s domain.AgentSpec) error {
	if s.Backend != "devin" {
		return nil
	}
	if strings.TrimSpace(s.StringOptions["model"]) == "" {
		return fmt.Errorf("devin: model is required")
	}
	if mode, raw := s.StringOptions["mode"], s.StringOptions["yolo"]; mode != "" && raw != "" {
		yolo, err := strconv.ParseBool(raw)
		if err == nil && yolo != (mode == "bypass") {
			return fmt.Errorf("devin: mode conflicts with explicit yolo")
		}
	}
	if raw, present := s.Options["yolo"]; present {
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("devin: yolo must be an unquoted boolean")
		}
	}
	for k, v := range s.StringOptions {
		switch k {
		case "command", "model", "reasoning":
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("devin: blank %s option", k)
			}
		case "speed":
			if v != "standard" && v != "fast" {
				return fmt.Errorf("devin: speed must be standard or fast")
			}
		case "mode":
			switch v {
			case "normal", "accept-edits", "ask", "plan", "smart", "bypass":
			default:
				return fmt.Errorf("devin: unsupported mode")
			}
		case "yolo":
			if _, e := strconv.ParseBool(v); e != nil {
				return fmt.Errorf("devin: yolo must be boolean")
			}
		default:
			return fmt.Errorf("devin: unsupported option %s", k)
		}
	}
	for k, values := range s.ListOptions {
		if k != "env" && k != "inherit_env" {
			return fmt.Errorf("devin: unsupported list option %s", k)
		}
		for _, v := range values {
			key := v
			if k == "env" {
				var ok bool
				key, _, ok = strings.Cut(v, "=")
				if !ok {
					return fmt.Errorf("devin: env requires NAME=value")
				}
			}
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(v, 0) {
				return fmt.Errorf("devin: invalid environment name")
			}
			if key == "DEVIN_REFUSAL_FALLBACK" || key == "DEVIN_MODEL" || key == "DEVIN_PERMISSION_MODE" || key == "DEVIN_SANDBOX" {
				return fmt.Errorf("devin: reserved environment override %s", key)
			}
		}
	}
	return nil
}
