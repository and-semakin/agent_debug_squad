package config

import (
	"fmt"
	"net/url"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
)

// ValidateOpenCodeSettings rejects ambiguous migration and settings that cannot
// be applied to a process we do not own. Values never appear in diagnostics.
func ValidateOpenCodeSettings(s domain.MachineBackendSettings) error {
	mode := s.Mode
	if mode == "" {
		mode = "managed"
	}
	if mode != "managed" && mode != "external" {
		return fmt.Errorf("opencode.mode must be managed or external")
	}
	if mode == "managed" && (s.BaseURL != "" || s.Declared["base_url"]) {
		return fmt.Errorf("opencode.base_url requires mode: external; remove base_url to use managed mode")
	}
	if mode == "external" {
		if s.BaseURL == "" {
			return fmt.Errorf("opencode external mode requires base_url")
		}
		for key, set := range map[string]bool{"command": s.Command != "", "proxy_url": s.ProxyURL != "", "no_proxy": s.NoProxy != "", "inherit_env": len(s.InheritEnv) > 0} {
			if set || s.Declared[key] {
				return fmt.Errorf("opencode external mode cannot apply %s; configure the external process instead", key)
			}
		}
	}
	if s.BaseURL != "" {
		u, err := url.Parse(s.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("opencode.base_url must be an http or https URL without credentials, query or fragment")
		}
	}
	return nil
}

// ValidateOpenCodeAgent runs for YAML, recovered specs and programmatic callers.
func ValidateOpenCodeAgent(spec domain.AgentSpec) error {
	if spec.Backend != "opencode" {
		return nil
	}
	for _, key := range []string{"command", "proxy_url", "no_proxy", "snapshot", "env", "inherit_env", "runtime_path", "ca_cert_file"} {
		_, raw := spec.Options[key]
		_, str := spec.StringOptions[key]
		_, list := spec.ListOptions[key]
		if raw || str || list {
			return fmt.Errorf("opencode agent option %s is process-level; configure backends.yaml instead", key)
		}
	}
	for _, key := range []string{"mode", "base_url"} {
		if _, ok := spec.ListOptions[key]; ok {
			return fmt.Errorf("opencode agent option %s must be a string", key)
		}
	}
	return nil
}
