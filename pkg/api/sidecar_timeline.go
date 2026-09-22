package api

// SidecarTimelineStatus is the most recent health transition observed for a
// sidecar. It is derived from the latest wake.sidecar_health frame in the
// timeline; init-exit and restart frames remain available in Events.
type SidecarTimelineStatus struct {
	At     string `json:"at"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// SidecarTimelineResponse is the wire shape for
// GET /v1/apps/{slug}/sidecars/{sidecar_name}/timeline.
// Events are ordered oldest-first so clients can render the complete
// lifecycle narrative, while Latest provides the current health snapshot
// without requiring the client to scan the event payloads.
type SidecarTimelineResponse struct {
	SidecarName string                 `json:"sidecar_name"`
	AppID       string                 `json:"app_id"`
	Latest      *SidecarTimelineStatus `json:"latest,omitempty"`
	Events      []WakeTimelineEvent    `json:"events"`
	NextCursor  string                 `json:"next_cursor,omitempty"`
	Limit       int                    `json:"limit"`
}
