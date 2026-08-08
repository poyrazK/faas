// wake_timeline.go — issue #517 / PR-C / ADR-064 — wake-timeline
// SDK surface.
//
// The wake-timeline endpoint
// (`GET /v1/apps/{slug}/wakes/{wake_id}/timeline`) returns the
// per-stage events for a single cold wake. The wire shape is
// distinct from the audit-events endpoint because:
//
//   - The 200-row over-read cap on /v1/audit-events assumes the
//     table is subject-pinned; the wake_id lives in the jsonb
//     `data` column and would force a sequential scan to filter.
//   - The audit endpoint is unkeyed (kind-prefix only); the
//     wake-timeline is keyed on a single canonical handle so the
//     partial index events_wake_id_idx (migrations/00113) can
//     serve it in O(frames-for-this-wake).
//   - The wake-timeline is a sub-resource of /v1/apps/{slug},
//     mirroring /v1/apps/{slug}/logs + /v1/apps/{slug}/metrics +
//     /v1/apps/{slug}/wake — the auth gate inherits from the
//     parent /v1/apps/* budget.
package api

// WakeTimelineEvent is one frame of the wake timeline. The shape
// matches the typed event payloads the producers write (see
// pkg/events/wake.go) — `at` is RFC 3339 UTC, `kind` is the
// canonical wake.* vocabulary (e.g. "wake.boot_started",
// "wake.readiness_200", "wake.proxy_first_byte"), `actor` is the
// daemon that wrote the row ("schedd", "vmmd", "gatewayd",
// "egress", "builderd", "apid"), and `data` is the producer-supplied
// payload (json object).
type WakeTimelineEvent struct {
	At    string         `json:"at"`
	Kind  string         `json:"kind"`
	Actor string         `json:"actor"`
	Data  map[string]any `json:"data"`
}

// WakeTimelineResponse is the wire shape for
// `GET /v1/apps/{slug}/wakes/{wake_id}/timeline`. Events is the
// ordered list (at ASC — forward narrative). NextCursor is the
// opaque RFC 3339 timestamp the client passes back as `?cursor=`
// to fetch the next page; empty when this is the last page. Limit
// echoes the effective limit applied by the handler (capped at
// the SDK's documented max).
type WakeTimelineResponse struct {
	WakeID     string              `json:"wake_id"`
	AppID      string              `json:"app_id"`
	Events     []WakeTimelineEvent `json:"events"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Limit      int                 `json:"limit"`
}
