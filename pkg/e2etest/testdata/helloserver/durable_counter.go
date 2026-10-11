package main

import (
	"encoding/json"
	"math"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// The fixture uses only the supplied state. No local counter, credentials,
// persistent disk or external effects contribute to a transition.
type counterEnvelope struct {
	ProtocolVersion int    `json:"protocol_version"`
	Event           string `json:"event"`
	Entity          struct {
		Namespace string `json:"namespace"`
	} `json:"entity"`
	State struct {
		Data json.RawMessage `json:"data"`
	} `json:"state"`
	Payload struct {
		Delta *int64 `json:"delta"`
	} `json:"payload"`
}

func serveDurableCounter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var call counterEnvelope
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxDurableEntityInvocationBytes)).Decode(&call); err != nil ||
		call.ProtocolVersion != api.DurableEntityProtocolVersion || call.Event != "invoke" ||
		call.Entity.Namespace != "counters" || call.Payload.Delta == nil {
		http.Error(w, "invalid entity envelope", http.StatusUnprocessableEntity)
		return
	}
	var next struct {
		Count int64 `json:"count"`
	}
	if len(call.State.Data) > 0 && string(call.State.Data) != "null" {
		if err := json.Unmarshal(call.State.Data, &next); err != nil {
			http.Error(w, "invalid counter state", http.StatusUnprocessableEntity)
			return
		}
	}
	delta := *call.Payload.Delta
	if delta > 0 && next.Count > math.MaxInt64-delta || delta < 0 && next.Count < math.MinInt64-delta {
		http.Error(w, "counter overflow", http.StatusUnprocessableEntity)
		return
	}
	next.Count += delta
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": next, "result": next})
}
