package zcode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

var errEvidenceUnknown = fmt.Errorf("zcode registry evidence is unknown")

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// registryView is the live selectable provider/model projection read through
// the guarded bridge. Raw registry objects stay in the host; this projection
// only carries the selectability evidence the pre-dispatch verification
// consumes.
type registryView struct {
	Revision string
	Models   map[string]registryModel
}

type registryModel struct {
	ProviderID      string
	ModelID         string
	ReasoningLevels []string
	Disabled        bool
	DisabledReason  string
}

// registrySelectionKey builds the canonical lookup key for a provider/model
// pair; reasoning matches case-insensitively.
func registrySelectionKey(providerID, modelID string) string {
	return strings.ToLower(providerID) + "\x00" + strings.ToLower(strings.TrimSpace(modelID))
}

// supportsReasoning reports whether the requested reasoning level is
// selectable for the model. An absent level list means unknown, never
// supported.
func (m registryModel) supportsReasoning(level string) bool {
	if m.ReasoningLevels == nil {
		return false
	}
	for _, candidate := range m.ReasoningLevels {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(level)) {
			return true
		}
	}
	return false
}

func decodeRegistryView(raw json.RawMessage) (registryView, error) {
	out := registryView{Models: map[string]registryModel{}}
	var wire struct {
		Revision  string `json:"revision"`
		Providers []struct {
			ProviderID string `json:"providerId"`
			Models     []struct {
				ModelID          string   `json:"modelId"`
				ReasoningLevels  []string `json:"reasoningLevels"`
				DefaultReasoning string   `json:"defaultReasoningLevel"`
				DisabledReason   *string  `json:"disabledReason"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return out, fmt.Errorf("%w: malformed registry view", errEvidenceUnknown)
	}
	out.Revision = strings.TrimSpace(wire.Revision)
	if out.Revision == "" {
		return out, fmt.Errorf("%w: registry view has no revision", errEvidenceUnknown)
	}
	for _, provider := range wire.Providers {
		if provider.ProviderID == "" {
			return out, fmt.Errorf("%w: registry provider without identity", errEvidenceUnknown)
		}
		for _, model := range provider.Models {
			if strings.TrimSpace(model.ModelID) == "" {
				return out, fmt.Errorf("%w: registry model without identity", errEvidenceUnknown)
			}
			entry := registryModel{
				ProviderID:      provider.ProviderID,
				ModelID:         model.ModelID,
				ReasoningLevels: model.ReasoningLevels,
				DisabledReason:  deref(model.DisabledReason),
			}
			entry.Disabled = entry.DisabledReason != ""
			out.Models[registrySelectionKey(provider.ProviderID, model.ModelID)] = entry
		}
	}
	return out, nil
}

// verifySelection confirms the exact configured provider/model/reasoning is
// selectable in the live registry view of the installed runtime, applying the
// account evidence that marks the provider entitled and current. The check is
// purely local: it never reads a Z.AI network origin, so account-service
// reachability cannot gate dispatch. Any unreadable or ambiguous view fails
// closed, and no substitute model or reasoning level is ever selected.
func verifySelection(ctx context.Context, host *hostBridge, provider, model, reasoning string) error {
	outcome := host.readRegistryView(ctx, overlayRequest{Provider: provider, Entitled: true, Current: true})
	if !outcome.ok {
		return fmt.Errorf("the live selectable registry view could not be read: %s", outcome.failure)
	}
	view, err := decodeRegistryView(outcome.payload)
	if err != nil {
		return fmt.Errorf("the live selectable registry view could not be decoded: %w", err)
	}
	entry, ok := view.Models[registrySelectionKey(provider, model)]
	if !ok || entry.Disabled {
		return fmt.Errorf("the configured provider does not supply the requested model %q", model)
	}
	if !entry.supportsReasoning(reasoning) {
		return fmt.Errorf("the requested reasoning level %q is not selectable for model %q", reasoning, model)
	}
	return nil
}
