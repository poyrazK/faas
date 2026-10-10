// adr: 942
package durableentity

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

type RestorePreview struct {
	CurrentVersion         uint64  `json:"current_version"`
	SourceVersion          uint64  `json:"source_version"`
	ExpectedVersionMatches bool    `json:"expected_version_matches"`
	CurrentSchemaVersion   *uint32 `json:"current_schema_version,omitempty"`
	SourceSchemaVersion    *uint32 `json:"source_schema_version,omitempty"`
	SchemaRelation         string  `json:"schema_relation"`
	Compatibility          string  `json:"compatibility"`
	AlarmPending           bool    `json:"alarm_pending"`
	OutboxPending          int     `json:"outbox_pending"`
	AlarmExhausted         bool    `json:"alarm_exhausted"`
	OutboxExhausted        bool    `json:"outbox_exhausted"`
}

// PreviewRestore is observational only; its comparison never authorizes restore.
// Application compatibility requires application validation, not schema equality.
func (m *Manager) PreviewRestore(ctx context.Context, id ID, expected uint64, exported StateExport) (RestorePreview, error) {
	if expected == 0 || !validStateExport(id, exported) {
		return RestorePreview{}, ErrInvalid
	}
	if len(exported.Data) > api.MaxDurableEntitySnapshotBytes {
		return RestorePreview{}, ErrLimit
	}
	base, _, err := m.readManifest(ctx, id)
	if err != nil {
		return RestorePreview{}, err
	}
	if base.Version == 0 {
		return RestorePreview{}, ErrNotFound
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return RestorePreview{}, err
	}
	// Detect publication/reclamation during the observation, without claims.
	latest, _, err := m.readManifest(ctx, id)
	if err != nil {
		return RestorePreview{}, err
	}
	if latest.Revision != base.Revision {
		return RestorePreview{}, ErrConflict
	}
	out := RestorePreview{CurrentVersion: state.Version, SourceVersion: exported.Version, ExpectedVersionMatches: expected == state.Version, CurrentSchemaVersion: applicationSchema(state.Data), SourceSchemaVersion: applicationSchema(exported.Data), SchemaRelation: "unknown", Compatibility: "unverified", AlarmPending: state.AlarmAt != nil, OutboxPending: len(state.Outbox)}
	health := healthSample(base, state)
	out.AlarmExhausted, out.OutboxExhausted = health.AlarmExhausted, health.OutboxExhausted
	if out.CurrentSchemaVersion != nil && out.SourceSchemaVersion != nil {
		out.SchemaRelation = "same"
		if *out.SourceSchemaVersion < *out.CurrentSchemaVersion {
			out.SchemaRelation = "older"
		}
		if *out.SourceSchemaVersion > *out.CurrentSchemaVersion {
			out.SchemaRelation = "newer"
		}
	}
	return out, nil
}

func applicationSchema(data json.RawMessage) *uint32 {
	var fields map[string]json.RawMessage
	var version uint32
	if json.Unmarshal(data, &fields) != nil || len(fields) != 2 || !json.Valid(fields["data"]) || json.Unmarshal(fields["schema_version"], &version) != nil || version == 0 {
		return nil
	}
	return &version
}
