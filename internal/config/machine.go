package config

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"gopkg.in/yaml.v3"
)

// MachineBackendsFileName is the machine-level backend settings file, read
// from the user's home directory next to the OpenRouter key file. It holds
// machine-specific settings (proxies, CA files, executable and runtime
// locations, and the judge confidence default) so squad YAML stays shareable.
const MachineBackendsFileName = "backends.yaml"

// MachineBackendsPath returns the default settings file location for a home
// directory.
func MachineBackendsPath(homeDir string) string {
	return filepath.Join(homeDir, ".agent-debug-squad", MachineBackendsFileName)
}

// supportedMachineKeys lists the keys one backend section accepts, in a
// stable order for error messages.
var supportedMachineKeys = map[string][]string{
	domain.MachineBackendCodex:    {"command", "inherit_env", "no_proxy", "proxy_url"},
	domain.MachineBackendCursor:   {"ca_cert_file", "command", "inherit_env", "no_proxy", "proxy_url"},
	domain.MachineBackendKimi:     {"command", "inherit_env", "no_proxy", "proxy_url"},
	domain.MachineBackendOpenCode: {"base_url", "command", "inherit_env", "mode", "no_proxy", "proxy_url", "snapshot"},
	domain.MachineBackendZCode:    {"ca_cert_file", "command", "inherit_env", "no_proxy", "proxy_url", "runtime_path"},
	domain.MachineBackendJudge:    {"confidence_threshold", "proxy_url"},
}

// stringMachineKeys are the keys decoded as plain strings.
var stringMachineKeys = map[string]bool{
	"base_url":     true,
	"mode":         true,
	"ca_cert_file": true,
	"command":      true,
	"runtime_path": true,
}

// LoadMachineBackends reads and validates the machine backend settings file.
// A missing or empty file yields empty settings without error; anything else
// must validate strictly, because a silently ignored typo in a rarely-edited
// file is worse than a startup failure that names the offending key.
func LoadMachineBackends(homeDir string) (domain.MachineBackends, error) {
	var backends domain.MachineBackends
	path := MachineBackendsPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return backends, nil
		}
		return backends, fmt.Errorf("read backends config %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return backends, nil
	}

	// Exactly one YAML document: content after a `---` separator must not
	// silently escape validation.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var sections map[string]yaml.Node
	if err := dec.Decode(&sections); err != nil {
		return backends, fmt.Errorf("backends config %s: %w", path, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return backends, fmt.Errorf("backends config %s: expected a single YAML document", path)
	}

	names := make([]string, 0, len(sections))
	for name := range sections {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		node := sections[name]
		settings, err := parseMachineSection(path, name, &node)
		if err != nil {
			return backends, err
		}
		if settings == nil {
			continue
		}
		switch name {
		case domain.MachineBackendCodex:
			backends.Codex = settings
		case domain.MachineBackendCursor:
			backends.Cursor = settings
		case domain.MachineBackendKimi:
			backends.Kimi = settings
		case domain.MachineBackendOpenCode:
			backends.OpenCode = settings
		case domain.MachineBackendZCode:
			backends.ZCode = settings
		case domain.MachineBackendJudge:
			backends.Judge = settings
		}
	}
	return backends, nil
}

// parseMachineSection validates one backend section node against its key
// allowlist and decodes the supported keys. A nil settings result means the
// section is present but empty.
func parseMachineSection(path, name string, node *yaml.Node) (*domain.MachineBackendSettings, error) {
	supported, ok := supportedMachineKeys[name]
	if !ok {
		return nil, fmt.Errorf("backends config %s: unknown backend section %q (supported: %s)",
			path, name, strings.Join(domain.MachineBackendNames(), ", "))
	}
	supportedSet := map[string]bool{}
	for _, key := range supported {
		supportedSet[key] = true
	}

	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return nil, fmt.Errorf("backends config %s: section %q: %w", path, name, err)
	}

	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !supportedSet[key] {
			return nil, fmt.Errorf("backends config %s: section %q: unsupported key %q (supported: %s)",
				path, name, key, strings.Join(supported, ", "))
		}
	}

	settings := &domain.MachineBackendSettings{}
	for _, key := range keys {
		value := fields[key]
		switch {
		case stringMachineKeys[key]:
			decoded, err := decodeMachineString(path, name, key, &value)
			if err != nil {
				return nil, err
			}
			switch key {
			case "mode":
				settings.Mode = decoded
			case "base_url":
				if decoded != "" {
					if err := validateMachineURL(path, name, key, decoded); err != nil {
						return nil, err
					}
				}
				settings.BaseURL = decoded
			case "ca_cert_file":
				settings.CACertFile = decoded
			case "command":
				settings.Command = decoded
			case "runtime_path":
				settings.RuntimePath = decoded
			}
		case key == "snapshot":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
				return nil, fmt.Errorf("backends config %s: section %q: key snapshot must be a boolean", path, name)
			}
			var snapshot bool
			if err := value.Decode(&snapshot); err != nil {
				return nil, fmt.Errorf("backends config %s: section %q: key snapshot must be a boolean", path, name)
			}
			settings.Snapshot = &snapshot
		case key == "proxy_url":
			decoded, err := decodeMachineString(path, name, key, &value)
			if err != nil {
				return nil, err
			}
			if decoded != "" {
				if err := validateMachineURL(path, name, key, decoded); err != nil {
					return nil, err
				}
			}
			settings.ProxyURL = decoded
		case key == "confidence_threshold":
			if value.Kind != yaml.ScalarNode || (value.Tag != "!!int" && value.Tag != "!!float") {
				return nil, fmt.Errorf("backends config %s: section %q: key %q must be a finite number greater than 0 and at most 1", path, name, key)
			}
			var threshold float64
			if err := value.Decode(&threshold); err != nil || math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold <= 0 || threshold > 1 {
				return nil, fmt.Errorf("backends config %s: section %q: key %q must be a finite number greater than 0 and at most 1", path, name, key)
			}
			settings.ConfidenceThreshold = &threshold
		case key == "no_proxy":
			noProxy, err := decodeNoProxy(path, name, &value)
			if err != nil {
				return nil, err
			}
			settings.NoProxy = noProxy
		case key == "inherit_env":
			inherit, err := decodeInheritEnv(path, name, &value)
			if err != nil {
				return nil, err
			}
			settings.InheritEnv = inherit
		}
	}
	if name == domain.MachineBackendOpenCode {
		settings.Declared = map[string]bool{}
		for _, key := range keys {
			settings.Declared[key] = true
		}
		if err := ValidateOpenCodeSettings(*settings); err != nil {
			return nil, fmt.Errorf("backends config %s: %w", path, err)
		}
	}
	if machineSectionEmpty(settings) {
		return nil, nil
	}
	return settings, nil
}

// decodeMachineString accepts only a !!str scalar and rejects every other
// tag (bool, int, null) as a wrong type, plus values that are blank after
// trimming. An explicit empty string stays valid and means "unset".
func decodeMachineString(path, section, key string, node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("backends config %s: section %q: key %q must be a string", path, section, key)
	}
	raw := node.Value
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" && raw != "" {
		return "", fmt.Errorf("backends config %s: section %q: key %q must not be blank", path, section, key)
	}
	return trimmed, nil
}

// validateMachineURL checks the http/https shape without echoing the value:
// proxy URLs may embed credentials, and validation errors reach stderr.
func validateMachineURL(path, section, key, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("backends config %s: section %q: key %q must be an http or https URL", path, section, key)
	}
	return nil
}

// decodeNoProxy accepts a comma-separated string, delivered verbatim, or a
// list of strings, joined with commas. Anything else is a type error.
func decodeNoProxy(path, name string, node *yaml.Node) (string, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag != "!!str" {
			return "", fmt.Errorf("backends config %s: section %q: key %q must be a string or list of strings", path, name, "no_proxy")
		}
		trimmed := strings.TrimSpace(node.Value)
		if trimmed == "" && node.Value != "" {
			return "", fmt.Errorf("backends config %s: section %q: key %q must not be blank", path, name, "no_proxy")
		}
		return trimmed, nil
	case yaml.SequenceNode:
		items, err := decodeStringSequence(path, name, "no_proxy", node)
		if err != nil {
			return "", err
		}
		return strings.Join(items, ","), nil
	default:
		return "", fmt.Errorf("backends config %s: section %q: key %q must be a string or list of strings", path, name, "no_proxy")
	}
}

func machineSectionEmpty(settings *domain.MachineBackendSettings) bool {
	return settings.Mode == "" && settings.Snapshot == nil && len(settings.Declared) == 0 && settings.Command == "" && settings.RuntimePath == "" && settings.BaseURL == "" &&
		settings.ProxyURL == "" && settings.NoProxy == "" && settings.CACertFile == "" &&
		len(settings.InheritEnv) == 0 && settings.ConfidenceThreshold == nil
}

// decodeInheritEnv accepts a list of strings or a comma-separated string of
// variable names. Empty names are rejected: they silently inherit nothing.
func decodeInheritEnv(path, name string, node *yaml.Node) ([]string, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag != "!!str" {
			return nil, fmt.Errorf("backends config %s: section %q: key %q must be a string or list of strings", path, name, "inherit_env")
		}
		if strings.TrimSpace(node.Value) == "" && node.Value != "" {
			return nil, fmt.Errorf("backends config %s: section %q: key %q must not be blank", path, name, "inherit_env")
		}
		return splitInheritNames(path, name, node.Value)
	case yaml.SequenceNode:
		return decodeStringSequence(path, name, "inherit_env", node)
	default:
		return nil, fmt.Errorf("backends config %s: section %q: key %q must be a string or list of strings", path, name, "inherit_env")
	}
}

func splitInheritNames(path, name, value string) ([]string, error) {
	items := strings.Split(value, ",")
	names := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			return nil, fmt.Errorf("backends config %s: section %q: key %q must not contain empty entries", path, name, "inherit_env")
		}
		names = append(names, trimmed)
	}
	return names, nil
}

// decodeStringSequence requires every element to be a non-blank !!str
// scalar; yaml.v3 would otherwise coerce bools and ints into strings.
func decodeStringSequence(path, name, key string, node *yaml.Node) ([]string, error) {
	items := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, fmt.Errorf("backends config %s: section %q: key %q must contain only strings", path, name, key)
		}
		trimmed := strings.TrimSpace(item.Value)
		if trimmed == "" {
			return nil, fmt.Errorf("backends config %s: section %q: key %q must not contain empty entries", path, name, key)
		}
		items = append(items, trimmed)
	}
	return items, nil
}

// MachineEnvEntries translates one backend's machine network settings into
// the environment variables that backend honors. The table mirrors what
// README "Environment And Secrets" documents for hand configuration:
// standard proxy variables for the CLIs that read them, NODE_USE_ENV_PROXY
// for the Node-based Cursor and Kimi CLIs, and the ZCode-specific variables
// because the ZCode runtime routes its traffic types differently and must
// not receive plain HTTP_PROXY from machine settings.
func MachineEnvEntries(backend string, settings *domain.MachineBackendSettings) []string {
	if settings == nil {
		return nil
	}
	var entries []string
	add := func(key, value string) {
		entries = append(entries, key+"="+value)
	}
	switch backend {
	case domain.MachineBackendCodex:
		if settings.ProxyURL != "" {
			add("HTTP_PROXY", settings.ProxyURL)
			add("HTTPS_PROXY", settings.ProxyURL)
		}
		if settings.NoProxy != "" {
			add("NO_PROXY", settings.NoProxy)
		}
	case domain.MachineBackendKimi:
		if settings.ProxyURL != "" {
			add("HTTP_PROXY", settings.ProxyURL)
			add("HTTPS_PROXY", settings.ProxyURL)
			add("NODE_USE_ENV_PROXY", "1")
		}
		if settings.NoProxy != "" {
			add("NO_PROXY", settings.NoProxy)
		}
	case domain.MachineBackendCursor:
		if settings.ProxyURL != "" {
			add("HTTP_PROXY", settings.ProxyURL)
			add("HTTPS_PROXY", settings.ProxyURL)
			add("NODE_USE_ENV_PROXY", "1")
		}
		if settings.NoProxy != "" {
			add("NO_PROXY", settings.NoProxy)
		}
		if settings.CACertFile != "" {
			add("NODE_EXTRA_CA_CERTS", settings.CACertFile)
		}
	case domain.MachineBackendZCode:
		if settings.ProxyURL != "" {
			add("ZCODE_HTTP_PROXY", settings.ProxyURL)
		}
		if settings.NoProxy != "" {
			add("ZCODE_NO_PROXY", settings.NoProxy)
		}
		if settings.CACertFile != "" {
			add("ZCODE_AGENT_CA_CERT", settings.CACertFile)
		}
	}
	return entries
}

// machineStringDefaults lists the agent option keys a backend may default
// from machine settings.
var machineStringDefaults = map[string][]string{
	domain.MachineBackendCodex:  {"command"},
	domain.MachineBackendCursor: {"command"},
	domain.MachineBackendKimi:   {"command"},
	domain.MachineBackendZCode:  {"command", "runtime_path"},
}

// MergeMachineDefaults applies machine backend settings to one agent spec as
// defaults below the agent's own options: string options are filled only
// when the agent sets none, machine inherit_env unions with the agent's own
// list (machine entries first, duplicates removed), and translated network
// entries are prepended to the explicit env list after suppressing any
// variable the agent defines in options.env or that the union inherit list
// names, so the built child environment never contains duplicate keys. The
// input spec's maps are never mutated; persisted snapshots keep storing only
// the agent's raw options.
func MergeMachineDefaults(spec domain.AgentSpec, backends domain.MachineBackends) domain.AgentSpec {
	if spec.Backend == domain.MachineBackendOpenCode {
		return spec
	}
	settings := backends.For(spec.Backend)
	if settings == nil {
		return spec
	}

	pendingDefaults := map[string]string{}
	for _, key := range machineStringDefaults[spec.Backend] {
		value := machineStringValue(key, settings)
		if value != "" && spec.StringOptions[key] == "" {
			pendingDefaults[key] = value
		}
	}
	if len(pendingDefaults) > 0 {
		if spec.StringOptions == nil {
			spec.StringOptions = map[string]string{}
		} else {
			spec.StringOptions = cloneStringOptions(spec.StringOptions)
		}
		for key, value := range pendingDefaults {
			spec.StringOptions[key] = value
		}
	}

	entries := MachineEnvEntries(spec.Backend, settings)
	mergedInherit, inheritChanged := unionStrings(settings.InheritEnv, spec.ListOptions["inherit_env"])
	suppressed := map[string]bool{}
	for _, entry := range spec.ListOptions["env"] {
		if key, _, ok := strings.Cut(entry, "="); ok {
			suppressed[key] = true
		}
	}
	for _, key := range mergedInherit {
		suppressed[key] = true
	}
	mergedEnv := make([]string, 0, len(entries)+len(spec.ListOptions["env"]))
	for _, entry := range entries {
		if key, _, ok := strings.Cut(entry, "="); ok && suppressed[key] {
			continue
		}
		mergedEnv = append(mergedEnv, entry)
	}
	mergedEnv = append(mergedEnv, spec.ListOptions["env"]...)
	envChanged := len(mergedEnv) != len(spec.ListOptions["env"])
	if !inheritChanged && !envChanged {
		return spec
	}
	if spec.ListOptions == nil {
		spec.ListOptions = map[string][]string{}
	} else {
		spec.ListOptions = cloneListOptions(spec.ListOptions)
	}
	if inheritChanged {
		spec.ListOptions["inherit_env"] = mergedInherit
	}
	if envChanged {
		spec.ListOptions["env"] = mergedEnv
	}
	return spec
}

// unionStrings merges a machine-level name list with an agent-level one:
// machine entries first, duplicates removed, agent order preserved. The
// boolean reports whether the machine list contributed anything.
func unionStrings(machine, agent []string) ([]string, bool) {
	if len(machine) == 0 {
		return agent, false
	}
	seen := make(map[string]bool, len(machine)+len(agent))
	merged := make([]string, 0, len(machine)+len(agent))
	for _, value := range machine {
		if !seen[value] {
			seen[value] = true
			merged = append(merged, value)
		}
	}
	for _, value := range agent {
		if !seen[value] {
			seen[value] = true
			merged = append(merged, value)
		}
	}
	return merged, true
}

func machineStringValue(key string, settings *domain.MachineBackendSettings) string {
	switch key {
	case "command":
		return settings.Command
	case "runtime_path":
		return settings.RuntimePath
	case "base_url":
		return settings.BaseURL
	default:
		return ""
	}
}

func cloneStringOptions(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source)+1)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneListOptions(source map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(source))
	for key, value := range source {
		cloned[key] = append([]string(nil), value...)
	}
	return cloned
}
