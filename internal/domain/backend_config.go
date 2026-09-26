package domain

import "sort"

// MachineBackendSettings holds machine-level defaults for one backend. Most
// values describe local backend locations and network settings; the judge can
// also set a local confidence default without changing shared workflow YAML.
type MachineBackendSettings struct {
	Mode     string
	Snapshot *bool
	// Declared records explicitly present machine keys, including empty values.
	Declared    map[string]bool
	Command     string
	RuntimePath string
	BaseURL     string
	ProxyURL    string
	NoProxy     string
	CACertFile  string
	// ConfidenceThreshold is the judge's machine default when a workflow
	// definition omits its own threshold. Nil means use the built-in default.
	ConfidenceThreshold *float64
	// InheritEnv names ambient variables to copy from the server process
	// into every child process of the backend as a default below agent
	// options. Values come from the server environment at dispatch time, so
	// the file itself never carries secrets. Managed OpenCode captures its
	// environment once per runtime and has no per-agent process overrides.
	InheritEnv []string
}

// MachineBackends is the resolved content of the machine backend settings
// file. Nil sections mean the backend has no machine settings.
type MachineBackends struct {
	Codex    *MachineBackendSettings
	Cursor   *MachineBackendSettings
	Kimi     *MachineBackendSettings
	OpenCode *MachineBackendSettings
	ZCode    *MachineBackendSettings
	Judge    *MachineBackendSettings
}

// Backend names accepted as sections in the machine settings file.
const (
	MachineBackendCodex    = "codex"
	MachineBackendCursor   = "cursor"
	MachineBackendKimi     = "kimi"
	MachineBackendOpenCode = "opencode"
	MachineBackendZCode    = "zcode"
	MachineBackendJudge    = "judge"
)

// MachineBackendNames lists the accepted section names in a stable order.
func MachineBackendNames() []string {
	return []string{
		MachineBackendCodex,
		MachineBackendCursor,
		MachineBackendKimi,
		MachineBackendOpenCode,
		MachineBackendZCode,
		MachineBackendJudge,
	}
}

// For returns the machine settings for a backend name, or nil when the
// backend has no machine settings or is not a machine-configurable backend.
func (m MachineBackends) For(backend string) *MachineBackendSettings {
	switch backend {
	case MachineBackendCodex:
		return m.Codex
	case MachineBackendCursor:
		return m.Cursor
	case MachineBackendKimi:
		return m.Kimi
	case MachineBackendOpenCode:
		return m.OpenCode
	case MachineBackendZCode:
		return m.ZCode
	case MachineBackendJudge:
		return m.Judge
	default:
		return nil
	}
}

// Configured returns the alphabetically sorted names of backends with a
// non-nil section. It backs the names-only startup log; values stay out of
// logs.
func (m MachineBackends) Configured() []string {
	var names []string
	for _, name := range MachineBackendNames() {
		if m.For(name) != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// JudgeProxyURL returns the machine-level default proxy for the judge, or
// empty when none is configured. The session YAML judge.proxy_url wins over
// this value.
func (m MachineBackends) JudgeProxyURL() string {
	if m.Judge == nil {
		return ""
	}
	return m.Judge.ProxyURL
}

// JudgeConfidenceThreshold returns the configured machine default, or zero
// when none is configured. Machine-file validation guarantees a present value
// is greater than zero and at most one.
func (m MachineBackends) JudgeConfidenceThreshold() float64 {
	if m.Judge == nil || m.Judge.ConfidenceThreshold == nil {
		return 0
	}
	return *m.Judge.ConfidenceThreshold
}
