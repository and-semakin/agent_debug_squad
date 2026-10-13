package config

import (
	"github.com/and-semakin/agent_debug_squad/internal/adapters/devin"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

func ValidateDevinAgent(s domain.AgentSpec) error { return devin.ValidateSpec(s) }
