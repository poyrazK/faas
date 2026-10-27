package api

import (
	"context"
	"net/http"
	"net/url"
)

// DatabaseSwitch is a rollback that restores the app's managed PostgreSQL
// database with the code (ADR-20261023181931): the database is restored to
// RestorePoint as a new database, and the app moves to it and to the target
// release while it is fenced.
type DatabaseSwitch struct {
	ID    string `json:"id"`
	AppID string `json:"app_id"`
	Scope string `json:"scope"`
	// Phase is restoring, staging, verifying, fencing, settling, draining,
	// switching, resuming, aborting, completed, failed or cancelled. From
	// fencing until completed the app does not serve.
	Phase string `json:"phase"`
	// FromDeploymentID served when the switch was requested; the switch
	// aborts if another release serves before it switches.
	FromDeploymentID   string `json:"from_deployment_id,omitempty"`
	FromRevision       int    `json:"from_revision,omitempty"`
	TargetDeploymentID string `json:"target_deployment_id"`
	TargetRevision     int    `json:"target_revision,omitempty"`
	// UndoneDeploymentIDs are the deployments whose release commands the
	// restore undoes; rolling back to them is refused afterwards.
	UndoneDeploymentIDs []string `json:"undone_deployment_ids"`
	// SourceDatabaseID keeps every write; the app leaves it.
	SourceDatabaseID   string `json:"source_database_id"`
	SourceDatabaseName string `json:"source_database_name,omitempty"`
	// TargetDatabaseID is the restored copy, once its restore has started.
	TargetDatabaseID string `json:"target_database_id,omitempty"`
	TargetName       string `json:"target_name"`
	// RestorePoint is the moment the copy is restored to: writes after it
	// stay only in the source database.
	RestorePoint string `json:"restore_point"`
	// Drain counts the fenced app's instances: retired with a native
	// receipt, parked (no process; snapshot invalidated), still pending, or
	// blocking the switch.
	Drain DatabaseSwitchDrain `json:"drain"`
	// FailureCode and FailureDetail explain a failed or cancelled switch,
	// and an abort in progress. LastErrorCode is the latest transient error
	// the switch is retrying.
	FailureCode       string `json:"failure_code,omitempty"`
	FailureDetail     string `json:"failure_detail,omitempty"`
	LastErrorCode     string `json:"last_error_code,omitempty"`
	RequestedBy       string `json:"requested_by,omitempty"`
	CancelRequestedAt string `json:"cancel_requested_at,omitempty"`
	FencedAt          string `json:"fenced_at,omitempty"`
	ReleaseSwitchedAt string `json:"release_switched_at,omitempty"`
	ActivatedAt       string `json:"activated_at,omitempty"`
	FinishedAt        string `json:"finished_at,omitempty"`
	PhaseStartedAt    string `json:"phase_started_at"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// DatabaseSwitchDrain counts a fenced app's instances by outcome.
type DatabaseSwitchDrain struct {
	Retired int `json:"retired"`
	Parked  int `json:"parked"`
	Pending int `json:"pending"`
	Blocked int `json:"blocked"`
}

// DatabaseSwitchList is an app's switches, newest first.
type DatabaseSwitchList struct {
	Items []DatabaseSwitch `json:"items"`
}

// ListDatabaseSwitches lists an app's database switches, newest first
// (ADR-20261023181931).
func (c *Client) ListDatabaseSwitches(ctx context.Context, slug string) ([]DatabaseSwitch, error) {
	var out DatabaseSwitchList
	if err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/database-switches", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// GetDatabaseSwitch reads one database switch.
func (c *Client) GetDatabaseSwitch(ctx context.Context, slug, id string) (DatabaseSwitch, error) {
	var out DatabaseSwitch
	return out, c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/database-switches/"+url.PathEscape(id), nil, &out)
}

// CancelDatabaseSwitch asks a switch to unwind; it is refused once the
// switch started switching the release and the bindings.
func (c *Client) CancelDatabaseSwitch(ctx context.Context, slug, id string) (DatabaseSwitch, error) {
	var out DatabaseSwitch
	return out, c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/database-switches/"+url.PathEscape(id)+"/cancel", nil, &out)
}

// DatabaseSwitchFinished reports whether a switch phase ends it.
func DatabaseSwitchFinished(phase string) bool {
	return phase == "completed" || phase == "failed" || phase == "cancelled"
}
