// adr: 570
package db

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

type TrafficSecurityChangedPayload struct {
	ScopeKind string `json:"scope_kind"`
	ScopeID   string `json:"scope_id"`
	Revision  int64  `json:"revision"`
}

func ParseTrafficSecurityChangedPayload(raw string) (TrafficSecurityChangedPayload, error) {
	var payload TrafficSecurityChangedPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return TrafficSecurityChangedPayload{}, fmt.Errorf("decode traffic security notification: %w", err)
	}
	id, err := uuid.Parse(payload.ScopeID)
	if err != nil || id == uuid.Nil || payload.Revision <= 0 ||
		(payload.ScopeKind != "account" && payload.ScopeKind != "app" && payload.ScopeKind != "deployment") {
		return TrafficSecurityChangedPayload{}, fmt.Errorf("invalid traffic security notification identity or generation")
	}
	payload.ScopeID = id.String()
	return payload, nil
}
