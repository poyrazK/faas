// pkg/api/canary/dto.go — wire-friendly DTOs for the canary ladder
// surface (issue #976 / ADR-122 / SAFE-RELEASES production-leveling
// Stream F). Lives in its own file so the spec_compliance_test
// schema scanner picks up only the wire DTO (CustomStage) and not
// the server-side domain types (Stage, Preset) that live alongside
// the catalog + LookupCustomPreset logic in preset.go. The Stage /
// Preset types are not on the wire — they're meterd-side domain
// objects walked by the canary orchestrator — so they don't need
// a matching openapi.yaml schema.
package canary

// CustomStage is the wire-friendly form of Stage (issue #976 /
// ADR-122 / SAFE-RELEASES production-leveling Stream F). The
// apid CreateDeploymentRequest path decodes --canary-stages
// "1@30s,10@2m,100@0" into []CustomStage; the handler then calls
// LookupCustomPreset to validate + synthesise a Preset for the
// DB row's canary_stages jsonb column.
//
// Wire shape (mirrored as `CustomStage` in api/openapi.yaml +
// pkg/apid/openapi.yaml, regenerated into sdk/node + sdk/python
// as `CustomStage`). The duration is intentionally a string in
// time.ParseDuration form (e.g. "30s", "2m", "0s" for the
// terminal hop) so a future per-account spec override
// (per-account spec at meterd runtime, gated on Enterprise
// plan) can reuse this DTO without a wire break.
type CustomStage struct {
	Percent     int                   `json:"percent"`
	Duration    string                `json:"duration"` // time.ParseDuration string form
	MirrorClean *MirrorCleanCondition `json:"mirror_clean,omitempty"`
}

// MirrorCleanCondition gates advancement out of a canary stage on a clean
// traffic mirror window. The condition is satisfied only after at least
// MinInvocations mirror comparisons have completed in the last WindowSeconds
// and every comparison is free of status, schema, body, and crash signals.
type MirrorCleanCondition struct {
	MinInvocations int `json:"min_invocations"`
	WindowSeconds  int `json:"window_s"`
}
