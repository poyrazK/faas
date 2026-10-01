// adr: 375
package trafficrevocation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type securityScopeWire struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// Only a protected platform hop may author this canonical, bounded snapshot.
// It carries no credentials and grants no access without an authoritative read.
func EncodeSnapshot(states map[Scope]State) (string, error) {
	if len(states) == 0 || len(states) > api.TrafficSecurityMaxRequestScopes {
		return "", ErrUnavailable
	}
	rows := make([]securityScopeWire, 0, len(states))
	for scope, state := range states {
		id, err := uuid.Parse(scope.ID)
		if err != nil || id == uuid.Nil || scope.ID != id.String() || state.Revision < 0 || state.Revoked ||
			(scope.Kind != "account" && scope.Kind != "app" && scope.Kind != "deployment") {
			return "", ErrUnavailable
		}
		rows = append(rows, securityScopeWire{scope.Kind, scope.ID, state.Revision})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Kind < rows[j].Kind || rows[i].Kind == rows[j].Kind && rows[i].ID < rows[j].ID
	})
	data, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	value := "v1." + base64.RawURLEncoding.EncodeToString(data)
	if len(value) > api.TrafficSecurityMaxHeaderBytes {
		return "", ErrUnavailable
	}
	return value, nil
}

func DecodeSnapshot(value string) (map[Scope]State, error) {
	if len(value) > api.TrafficSecurityMaxHeaderBytes || !strings.HasPrefix(value, "v1.") {
		return nil, ErrUnavailable
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(value, "v1."))
	if err != nil {
		return nil, ErrUnavailable
	}
	var rows []securityScopeWire
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) == 0 || len(rows) > api.TrafficSecurityMaxRequestScopes {
		return nil, ErrUnavailable
	}
	states := make(map[Scope]State, len(rows))
	for _, row := range rows {
		scope := Scope{Kind: row.Kind, ID: row.ID}
		if _, duplicate := states[scope]; duplicate {
			return nil, ErrUnavailable
		}
		states[scope] = State{Revision: row.Revision}
	}
	canonical, err := EncodeSnapshot(states)
	if err != nil || canonical != value {
		return nil, ErrUnavailable
	}
	return states, nil
}

// EncodeAdmittedSnapshot normalizes trusted store identities for transport.
// Registry ownership and its original keys remain unchanged.
func EncodeAdmittedSnapshot(states map[Scope]State) (string, error) {
	if len(states) == 0 || len(states) > api.TrafficSecurityMaxRequestScopes {
		return "", ErrUnavailable
	}
	canonical := make(map[Scope]State, len(states))
	for scope, state := range states {
		id, err := uuid.Parse(scope.ID)
		if err != nil || id == uuid.Nil {
			return "", ErrUnavailable
		}
		scope.ID = id.String()
		if before, exists := canonical[scope]; exists && before != state {
			return "", ErrUnavailable
		}
		canonical[scope] = state
	}
	return EncodeSnapshot(canonical)
}

type handoffSnapshotKey struct{}
type handoffSnapshot struct {
	value  string
	states map[Scope]State
}

// WithHandoffSnapshot attaches bounded, canonical metadata from a trusted
// transport. The receiving owner must still verify identity and generations.
func WithHandoffSnapshot(ctx context.Context, value string) (context.Context, error) {
	states, err := DecodeSnapshot(value)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, handoffSnapshotKey{}, handoffSnapshot{value: value, states: states}), nil
}

func HandoffSnapshot(ctx context.Context) (map[Scope]State, bool) {
	snapshot, ok := ctx.Value(handoffSnapshotKey{}).(handoffSnapshot)
	return maps.Clone(snapshot.states), ok
}

func HandoffValue(ctx context.Context) string {
	snapshot, _ := ctx.Value(handoffSnapshotKey{}).(handoffSnapshot)
	return snapshot.value
}
