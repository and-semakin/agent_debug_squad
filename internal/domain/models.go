package domain

import "time"

// ModelDiscoveryInput is private invocation state, never a public catalog.
type ModelDiscoveryInput struct {
	WorkspaceDir  string   `json:"-"`
	AmbientEnv    []string `json:"-"`
	IncludeHidden bool     `json:"-"`
}
type ModelCatalog struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   time.Time              `json:"generated_at"`
	Status        string                 `json:"status"`
	Results       []ModelDiscoveryResult `json:"results"`
	Error         *ModelCatalogError     `json:"error,omitempty"`
}
type ModelCatalogError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ModelDiscoveryResult struct {
	TargetID              string            `json:"target_id"`
	Backend               string            `json:"backend"`
	Agents                []string          `json:"agents"`
	Status                string            `json:"status"`
	Complete              bool              `json:"complete"`
	Models                []CatalogModel    `json:"models"`
	Sources               []ModelSource     `json:"sources"`
	Diagnostics           []ModelDiagnostic `json:"diagnostics"`
	InferenceVerification string            `json:"inference_verification"`
}
type ModelDiagnostic struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	Phase           string `json:"phase,omitempty"`
	RestartRequired bool   `json:"restart_required,omitempty"`
}
type ModelSource struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	Operation    string         `json:"operation"`
	Version      string         `json:"version,omitempty"`
	RetrievedAt  time.Time      `json:"retrieved_at"`
	Scope        string         `json:"scope"`
	Complete     bool           `json:"complete"`
	HiddenPolicy HiddenPolicy   `json:"hidden_policy"`
	Freshness    ModelFreshness `json:"freshness"`
}
type HiddenPolicy struct {
	Requested string `json:"requested"`
	Applied   string `json:"applied"`
}
type ModelFreshness struct {
	State  string     `json:"state"`
	AsOf   *time.Time `json:"as_of,omitempty"`
	Cached *bool      `json:"cached,omitempty"`
}
type CatalogModel struct {
	ModelID         string           `json:"model_id"`
	ProviderID      string           `json:"provider_id,omitempty"`
	ProviderLabel   string           `json:"provider_label,omitempty"`
	DisplayName     string           `json:"display_name,omitempty"`
	Alias           string           `json:"alias,omitempty"`
	CatalogID       string           `json:"catalog_id,omitempty"`
	Configured      *bool            `json:"configured"`
	Connected       *bool            `json:"connected"`
	Listed          bool             `json:"listed"`
	DisabledReason  string           `json:"disabled_reason,omitempty"`
	Hidden          *bool            `json:"hidden,omitempty"`
	IsDefault       *bool            `json:"is_default,omitempty"`
	InputModalities []string         `json:"input_modalities,omitempty"`
	ContextWindow   *int64           `json:"context_window,omitempty"`
	Parameters      []ModelParameter `json:"parameters,omitempty"`
	Selection       ModelSelection   `json:"selection"`
	Sources         []string         `json:"sources"`
}
type ModelParameter struct {
	NativeName  string   `json:"native_name"`
	Values      []string `json:"values,omitempty"`
	Default     string   `json:"default,omitempty"`
	SquadOption string   `json:"squad_option,omitempty"`
}
type ModelSelection struct {
	Backend   string            `json:"backend"`
	Options   map[string]string `json:"options"`
	Supported bool              `json:"supported"`
	Reason    string            `json:"reason,omitempty"`
}
