package zcode

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Normalized, allowlisted account evidence decoded from the source-defined
// Z.AI read operations. Raw billing payloads never leave the host; the host
// forwards only these bounded projections, and unknown evidence is never
// coerced into a usable value.

var errEvidenceUnknown = fmt.Errorf("zcode account evidence is unknown")

// startBucket is one per-model allowance bucket of the Start Plan balance
// response. Amounts are only usable when the payload supplies finite numeric
// evidence; absent, null, or non-finite values stay unknown and never default
// to zero or exhaustion.
type startBucket struct {
	BucketID      string
	UserPlanID    string
	EntitlementID string
	Model         string
	UnitType      string
	Capabilities  []string
	hasAvailable  bool
	hasReserved   bool
	hasRemaining  bool
	available     float64
	reserved      float64
	remaining     float64
	ExpiresAt     time.Time
	hasExpiry     bool
	PeriodStart   time.Time
	PeriodEnd     time.Time
}

type startPlan struct {
	UserPlanID string
	PlanID     string
	Status     string
	EndsAt     time.Time
	hasEnd     bool
}

type startBalance struct {
	ServerTime time.Time
	Plans      []startPlan
	Buckets    []startBucket
	// hasPlans and hasBalances distinguish an authoritative empty list from a
	// missing one: a payload without the arrays stays unknown.
	hasPlans    bool
	hasBalances bool
}

// decodeStartBalance projects the bounded balance response onto the
// allowlisted evidence struct. It rejects non-finite amounts, negative or
// otherwise contradictory values, and unsupported unit types by reporting
// them as unknown evidence rather than guessing.
func decodeStartBalance(raw json.RawMessage) (startBalance, error) {
	var out startBalance
	var wire struct {
		Data *struct {
			ServerTime *float64          `json:"server_time"`
			Plans      []json.RawMessage `json:"plans"`
			Balances   []json.RawMessage `json:"balances"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return out, fmt.Errorf("%w: malformed balance payload", errEvidenceUnknown)
	}
	if wire.Data == nil {
		return out, fmt.Errorf("%w: balance payload has no data", errEvidenceUnknown)
	}
	if wire.Data.ServerTime != nil && *wire.Data.ServerTime >= 0 && !math.IsInf(*wire.Data.ServerTime, 0) {
		out.ServerTime = time.Unix(int64(*wire.Data.ServerTime), 0).UTC()
	}
	out.hasPlans = wire.Data.Plans != nil
	out.hasBalances = wire.Data.Balances != nil
	decodeNumber := func(raw json.RawMessage) (float64, bool) {
		if len(raw) == 0 || string(raw) == "null" {
			return 0, false
		}
		var asNumber float64
		if err := json.Unmarshal(raw, &asNumber); err == nil {
			if math.IsNaN(asNumber) || math.IsInf(asNumber, 0) {
				return 0, false
			}
			return asNumber, true
		}
		var asString string
		if err := json.Unmarshal(raw, &asString); err == nil {
			trimmed := strings.TrimSpace(asString)
			if trimmed == "" {
				return 0, false
			}
			var parsed float64
			if _, err := fmt.Sscanf(trimmed, "%g", &parsed); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
				return parsed, true
			}
		}
		return 0, false
	}
	decodeTime := func(raw json.RawMessage) (time.Time, bool) {
		seconds, ok := decodeNumber(raw)
		if !ok || seconds <= 0 {
			return time.Time{}, false
		}
		return time.Unix(int64(seconds), 0).UTC(), true
	}
	decodeString := func(raw json.RawMessage) string {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return ""
		}
		return value
	}
	for _, item := range wire.Data.Plans {
		var fields struct {
			UserPlanID json.RawMessage `json:"user_plan_id"`
			PlanID     json.RawMessage `json:"plan_id"`
			Status     json.RawMessage `json:"status"`
			EndsAt     json.RawMessage `json:"ends_at"`
		}
		if err := json.Unmarshal(item, &fields); err != nil {
			return out, fmt.Errorf("%w: malformed plan record", errEvidenceUnknown)
		}
		plan := startPlan{
			UserPlanID: decodeString(fields.UserPlanID),
			PlanID:     decodeString(fields.PlanID),
			Status:     strings.ToLower(strings.TrimSpace(decodeString(fields.Status))),
		}
		plan.EndsAt, plan.hasEnd = decodeTime(fields.EndsAt)
		out.Plans = append(out.Plans, plan)
	}
	for _, item := range wire.Data.Balances {
		var fields struct {
			BucketID      json.RawMessage `json:"bucket_id"`
			UserPlanID    json.RawMessage `json:"user_plan_id"`
			EntitlementID json.RawMessage `json:"entitlement_id"`
			ShowName      json.RawMessage `json:"show_name"`
			UnitType      json.RawMessage `json:"unit_type"`
			Capabilities  []string        `json:"capabilities"`
			Reserved      json.RawMessage `json:"reserved_units"`
			Remaining     json.RawMessage `json:"remaining_units"`
			Available     json.RawMessage `json:"available_units"`
			PeriodStart   json.RawMessage `json:"period_start"`
			PeriodEnd     json.RawMessage `json:"period_end"`
			ExpiresAt     json.RawMessage `json:"expires_at"`
		}
		if err := json.Unmarshal(item, &fields); err != nil {
			return out, fmt.Errorf("%w: malformed balance bucket", errEvidenceUnknown)
		}
		bucket := startBucket{
			BucketID:      decodeString(fields.BucketID),
			UserPlanID:    decodeString(fields.UserPlanID),
			EntitlementID: decodeString(fields.EntitlementID),
			UnitType:      decodeString(fields.UnitType),
			Capabilities:  fields.Capabilities,
		}
		bucket.Model = startBucketModel(bucket.Capabilities, decodeString(fields.ShowName))
		if bucket.UnitType != "" && !strings.EqualFold(bucket.UnitType, "token") {
			return out, fmt.Errorf("%w: unsupported balance unit %q", errEvidenceUnknown, bucket.UnitType)
		}
		bucket.available, bucket.hasAvailable = decodeNumber(fields.Available)
		bucket.reserved, bucket.hasReserved = decodeNumber(fields.Reserved)
		bucket.remaining, bucket.hasRemaining = decodeNumber(fields.Remaining)
		bucket.ExpiresAt, bucket.hasExpiry = decodeTime(fields.ExpiresAt)
		bucket.PeriodStart, _ = decodeTime(fields.PeriodStart)
		bucket.PeriodEnd, _ = decodeTime(fields.PeriodEnd)
		// Contradictory evidence stays unknown: negative amounts make the
		// bucket unusable for a spend decision.
		for _, amount := range []struct {
			value   float64
			present bool
		}{{bucket.available, bucket.hasAvailable}, {bucket.reserved, bucket.hasReserved}, {bucket.remaining, bucket.hasRemaining}} {
			if amount.present && amount.value < 0 {
				return out, fmt.Errorf("%w: contradictory negative amount in bucket %q", errEvidenceUnknown, bucket.BucketID)
			}
		}
		out.Buckets = append(out.Buckets, bucket)
	}
	return out, nil
}

// startBucketModel resolves the bucket's model identity the way the source
// does: model: capabilities entries win, the display name is only a fallback.
// Matching stays case-insensitive and trimmed; the original spelling is kept
// for selection.
func startBucketModel(capabilities []string, showName string) string {
	for _, capability := range capabilities {
		trimmed := strings.TrimSpace(capability)
		if strings.HasPrefix(strings.ToLower(trimmed), "model:") {
			if model := strings.TrimSpace(trimmed[len("model:"):]); model != "" {
				return model
			}
		}
	}
	return strings.TrimSpace(showName)
}

// active returns whether the plan is active at the paired server time. An
// expired end timestamp demotes the plan the way the source decoder does.
func (p startPlan) active(now time.Time) bool {
	status := strings.ToLower(strings.TrimSpace(p.Status))
	if status == "expired" {
		return false
	}
	if p.hasEnd && !p.EndsAt.IsZero() && !p.EndsAt.After(now) {
		return false
	}
	return status == "active" || status == ""
}

// spendable reports positive available quota for the requested model with
// valid period evidence at the given moment. The display percentage and the
// remaining balance never substitute for missing available evidence.
func (b startBucket) spendable(now time.Time) (bool, bool) {
	if b.Model == "" || !b.hasAvailable {
		return false, false
	}
	if b.hasExpiry && !b.ExpiresAt.IsZero() && !b.ExpiresAt.After(now) {
		return false, false
	}
	if !b.PeriodEnd.IsZero() && !b.PeriodEnd.After(now) {
		return false, false
	}
	return b.available > 0, true
}

// busy reports reservation pressure: occupied balance that has not actually
// run out. This is temporary, never exhaustion.
func (b startBucket) busy() (bool, bool) {
	if !b.hasAvailable || !b.hasRemaining {
		return false, false
	}
	return b.available <= 0 && b.remaining > 0, true
}

// exhausted reports confirmed zero remaining balance, the only bucket state
// that may route away from Start as exhausted.
func (b startBucket) exhausted() (bool, bool) {
	if !b.hasRemaining {
		return false, false
	}
	return b.remaining <= 0, true
}

type subscriptionState int

const (
	subscriptionUnknown subscriptionState = iota
	subscriptionActive
	subscriptionUnavailable
)

// decodeIndividualSubscription applies the inspected upstream rule for an
// active personal Coding subscription: a Coding product with status VALID and
// inCurrentPeriod true. Malformed relevant records stay unknown; a complete
// list without an active Coding subscription means unavailable.
func decodeIndividualSubscription(raw json.RawMessage) (subscriptionState, error) {
	var wire struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return subscriptionUnknown, fmt.Errorf("%w: malformed subscription payload", errEvidenceUnknown)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(wire.Data, &list); err != nil {
		var single struct {
			List []json.RawMessage `json:"list"`
		}
		if err := json.Unmarshal(wire.Data, &single); err != nil || single.List == nil {
			return subscriptionUnknown, fmt.Errorf("%w: unreadable subscription list", errEvidenceUnknown)
		}
		list = single.List
	}
	sawCoding := false
	for _, item := range list {
		var fields struct {
			Status          json.RawMessage `json:"status"`
			InCurrentPeriod *bool           `json:"inCurrentPeriod"`
			ProductID       *string         `json:"productId"`
			ProductName     *string         `json:"productName"`
		}
		if err := json.Unmarshal(item, &fields); err != nil {
			return subscriptionUnknown, fmt.Errorf("%w: malformed subscription record", errEvidenceUnknown)
		}
		var status string
		_ = json.Unmarshal(fields.Status, &status)
		if !isCodingProduct(deref(fields.ProductID), deref(fields.ProductName)) {
			continue
		}
		sawCoding = true
		if fields.InCurrentPeriod == nil {
			// A Coding record without the period flag is malformed relevant
			// evidence: unknown, never silently unavailable.
			return subscriptionUnknown, nil
		}
		if strings.EqualFold(strings.TrimSpace(status), "VALID") && *fields.InCurrentPeriod {
			return subscriptionActive, nil
		}
	}
	if !sawCoding {
		return subscriptionUnavailable, nil
	}
	return subscriptionUnavailable, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func isCodingProduct(productID, productName string) bool {
	return strings.Contains(strings.ToLower(productID), "coding") || strings.Contains(strings.ToLower(productName), "coding")
}

// registryView is the live selectable provider/model projection read through
// the guarded bridge. Raw registry objects stay in the host; this projection
// only carries the selectability evidence the routing rules consume.
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
