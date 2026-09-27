package modelprobe

import (
	"encoding/json"
	"github.com/and-semakin/agent_debug_squad/internal/domain"
	"time"
)

// Freshness accepts explicit source-owned evidence only. Retrieval time is never
// substituted for an absent source timestamp. Other response fields stay private.
func Freshness(r *domain.ModelDiscoveryResult, source int, body []byte) {
	var envelope struct {
		Freshness *struct {
			State  string     `json:"state"`
			AsOf   *time.Time `json:"as_of"`
			Cached *bool      `json:"cached"`
		} `json:"freshness"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Freshness == nil {
		return
	}
	f := envelope.Freshness
	if f.State != "unknown" && f.State != "fresh" && f.State != "stale" {
		return
	}
	if f.AsOf != nil {
		v := f.AsOf.UTC()
		f.AsOf = &v
	}
	value := domain.ModelFreshness{State: f.State, AsOf: f.AsOf, Cached: f.Cached}
	old := r.Sources[source].Freshness
	if old.State != "unknown" || old.AsOf != nil || old.Cached != nil {
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(value)
		if string(a) != string(b) {
			Fail(r, "partial", "conflicting_freshness")
			return
		}
	}
	r.Sources[source].Freshness = value
}
