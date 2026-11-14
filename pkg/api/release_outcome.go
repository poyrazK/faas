package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ReleaseOutcomeCheckNoteMax bounds the note a person records with a release
// outcome check (ADR-1448 amendment 1).
const ReleaseOutcomeCheckNoteMax = 1000

// RecordReleaseOutcomeCheckRequest records that a person checked the
// database after a release command whose outcome is unknown.
type RecordReleaseOutcomeCheckRequest struct {
	// Note says what was checked, for example "migration 42 applied; schema matches".
	Note string `json:"note"`
}

// Validate checks the note.
func (r RecordReleaseOutcomeCheckRequest) Validate() error {
	note := strings.TrimSpace(r.Note)
	if note == "" {
		return errors.New("note is required: say what you checked in the database")
	}
	if len(note) > ReleaseOutcomeCheckNoteMax {
		return errors.New("note must be at most 1000 bytes")
	}
	return nil
}

// ReleaseOutcomeCheck is a recorded check of a release command whose
// outcome was unknown. Later release commands of the app scope run again.
type ReleaseOutcomeCheck struct {
	DeploymentID string    `json:"deployment_id"`
	TaskID       string    `json:"task_id"`
	TaskStatus   string    `json:"task_status"`
	CheckedBy    string    `json:"checked_by"`
	Note         string    `json:"note"`
	CheckedAt    time.Time `json:"checked_at"`
}

// RecordReleaseOutcomeCheck records that the database was checked after the
// deployment's release command ended without an exit code.
func (c *Client) RecordReleaseOutcomeCheck(ctx context.Context, deploymentID string, req RecordReleaseOutcomeCheckRequest) (ReleaseOutcomeCheck, error) {
	var out ReleaseOutcomeCheck
	return out, c.do(ctx, http.MethodPost, "/v1/deployments/"+url.PathEscape(deploymentID)+"/release-outcome", req, &out)
}

// ErrReleaseOutcomeKnown is the 409 for a check of a release command whose
// outcome is known, or that was checked already.
func ErrReleaseOutcomeKnown(detail string) *Problem {
	return NewProblem(http.StatusConflict, CodeConflict, "Nothing to check", detail)
}
