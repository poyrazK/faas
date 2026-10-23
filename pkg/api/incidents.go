package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Unified incident sources and statuses (ADR-20261022193512). The sets are
// closed; clients may rely on them.
const (
	IncidentSourceAlert        = "alert"
	IncidentSourceRouteMonitor = "route_monitor"
	IncidentSourcePlatform     = "platform"

	IncidentStatusOpen     = "open"
	IncidentStatusResolved = "resolved"
)

// Incident is one entry of the unified incident list: an alert rule's firing
// period, a route monitor incident, or a platform incident from the status
// page. The id carries its source.
type Incident struct {
	ID         string     `json:"id"`
	Source     string     `json:"source"`
	Status     string     `json:"status"`
	Title      string     `json:"title"`
	App        string     `json:"app,omitempty"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	// RuleID and Metric name an alert incident's rule.
	RuleID string `json:"rule_id,omitempty"`
	Metric string `json:"metric,omitempty"`
	// DeploymentID is the deployment a route monitor incident watched.
	DeploymentID string `json:"deployment_id,omitempty"`
	// Impact is a platform incident's status page impact.
	Impact string `json:"impact,omitempty"`
	// AcknowledgedAt and AcknowledgedBy say who in the account took the
	// incident on, and when.
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
}

// Incident note kinds. The set is closed; clients may rely on it.
const (
	IncidentNoteKindAcknowledged = "acknowledged"
	IncidentNoteKindNote         = "note"
)

// IncidentNote is one entry of an incident's notes: an acknowledgement
// (no body) or a note. Notes are append-only.
type IncidentNote struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body,omitempty"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

// IncidentNoteRequest is POST /v1/incidents/{id}/notes.
type IncidentNoteRequest struct {
	Body string `json:"body"`
}

// IncidentsResponse is GET /v1/incidents: everything open plus what resolved
// since Since, open first, then newest first, capped at IncidentsMaxEntries.
type IncidentsResponse struct {
	Since     time.Time  `json:"since"`
	Incidents []Incident `json:"incidents"`
	Truncated bool       `json:"truncated"`
	// UnavailableSources names sources that could not be read; their
	// incidents are missing rather than absent.
	UnavailableSources []string `json:"unavailable_sources"`
}

// IncidentFire is one notification an alert incident sent.
type IncidentFire struct {
	At       time.Time `json:"at"`
	Observed float64   `json:"observed"`
	Status   string    `json:"status"`
}

// IncidentUpdate is one status page update of a platform incident.
type IncidentUpdate struct {
	At      time.Time `json:"at"`
	State   string    `json:"state"`
	Message string    `json:"message"`
}

// IncidentDetail is GET /v1/incidents/{id}: the incident and the evidence
// the platform has for it.
type IncidentDetail struct {
	Incident Incident `json:"incident"`
	// Fires are an alert incident's notifications, newest first, capped at
	// IncidentFiresMax.
	Fires []IncidentFire `json:"fires,omitempty"`
	// LogContext is what the logs showed at the newest fire
	// (ADR-20261022172052), when log search was on.
	LogContext json.RawMessage `json:"log_context,omitempty"`
	// Updates are a platform incident's status page updates, oldest first.
	Updates []IncidentUpdate `json:"updates,omitempty"`
	// Changes is the app's change timeline (ADR-751) from
	// IncidentChangeLookback before the incident opened until it resolved.
	Changes          []AppChangeEvent `json:"changes,omitempty"`
	ChangesTruncated bool             `json:"changes_truncated,omitempty"`
	// Notes are the account's acknowledgement and notes, oldest first.
	Notes []IncidentNote `json:"notes"`
	// UnavailableSources names evidence that could not be read.
	UnavailableSources []string `json:"unavailable_sources"`
}

// IncidentLogSummary summarises an alert incident's log context
// (ADR-20261022172052) the way its notification does, and returns the log
// search it links to. Both are empty when there is no log context.
func IncidentLogSummary(raw json.RawMessage) (line, searchPath string) {
	var lc struct {
		Errors            int    `json:"errors"`
		Warnings          int    `json:"warnings"`
		HTTP5xx           int    `json:"http_5xx"`
		SearchPath        string `json:"search_path"`
		BusiestErrorRoute *struct {
			Route string `json:"route"`
			Count int    `json:"count"`
		} `json:"busiest_error_route"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &lc) != nil {
		return "", ""
	}
	line = fmt.Sprintf("%d errors, %d warnings, %d HTTP 5xx", lc.Errors, lc.Warnings, lc.HTTP5xx)
	if r := lc.BusiestErrorRoute; r != nil && r.Route != "" {
		line += fmt.Sprintf(" (most errors on %s: %d)", r.Route, r.Count)
	}
	return line, lc.SearchPath
}

// IncidentsQuery filters GET /v1/incidents. Zero values select the server
// defaults: every app, open and resolved, the last IncidentsDefaultWindow.
type IncidentsQuery struct {
	App      string
	OpenOnly bool
	Since    time.Time
}

// ListIncidents runs GET /v1/incidents.
func (c *Client) ListIncidents(ctx context.Context, q IncidentsQuery) (IncidentsResponse, error) {
	var out IncidentsResponse
	v := url.Values{}
	if q.App != "" {
		v.Set("app", q.App)
	}
	if q.OpenOnly {
		v.Set("status", IncidentStatusOpen)
	}
	if !q.Since.IsZero() {
		v.Set("since", q.Since.UTC().Format(time.RFC3339))
	}
	path := "/v1/incidents"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetIncident runs GET /v1/incidents/{id}.
func (c *Client) GetIncident(ctx context.Context, id string) (IncidentDetail, error) {
	var out IncidentDetail
	return out, c.do(ctx, "GET", "/v1/incidents/"+url.PathEscape(id), nil, &out)
}

// AcknowledgeIncident runs POST /v1/incidents/{id}/acknowledge; acknowledging
// again returns the first acknowledgement.
func (c *Client) AcknowledgeIncident(ctx context.Context, id string) (IncidentNote, error) {
	var out IncidentNote
	return out, c.do(ctx, "POST", "/v1/incidents/"+url.PathEscape(id)+"/acknowledge", nil, &out)
}

// AddIncidentNote runs POST /v1/incidents/{id}/notes.
func (c *Client) AddIncidentNote(ctx context.Context, id, body string) (IncidentNote, error) {
	var out IncidentNote
	return out, c.do(ctx, "POST", "/v1/incidents/"+url.PathEscape(id)+"/notes", IncidentNoteRequest{Body: body}, &out)
}

// ErrIncidentResolved refuses to acknowledge a resolved incident.
func ErrIncidentResolved() *Problem {
	return NewProblem(http.StatusConflict, "incident_resolved", "Incident resolved",
		"the incident has already resolved, so there is nothing to acknowledge").
		WithDocs(docsBase + "/incidents").WithHint("add a note instead with `gregale incidents note`")
}

// ErrIncidentNotesLimit reports the per-incident note cap.
func ErrIncidentNotesLimit(limit, observed int) *Problem {
	return NewProblem(http.StatusConflict, "incident_notes_limit", "Incident note limit reached",
		fmt.Sprintf("the incident already has %d of %d notes from this account", observed, limit)).
		WithLimit(int64(limit), int64(observed)).WithDocs(docsBase + "/incidents")
}

// ErrIncidentsUnavailable is the FAAS_INCIDENTS_ENABLED gate's answer.
func ErrIncidentsUnavailable() *Problem {
	return NewProblem(http.StatusServiceUnavailable, "incidents_unavailable", "Incidents unavailable",
		"the unified incidents preview is not enabled")
}

// ErrIncidentNotFound names an incident the account cannot see.
func ErrIncidentNotFound() *Problem {
	return NewProblem(http.StatusNotFound, "incident_not_found", "Incident not found",
		"the account has no such incident").WithDocs(docsBase + "/incidents").WithHint("list them with `gregale incidents`")
}

// ErrIncidentsInvalid rejects a malformed incident query.
func ErrIncidentsInvalid(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeValidation, "Invalid incident query", detail).WithDocs(docsBase + "/incidents")
}
