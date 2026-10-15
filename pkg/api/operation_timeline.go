// ADR-1195: one chronological history of an Operation, merged from the
// records that already own each fact.
package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Timeline entry kinds, in tie-break order for entries with equal times.
const (
	OperationTimelineKindEvent    = "event"
	OperationTimelineKindAttempt  = "attempt"
	OperationTimelineKindStep     = "step_attempt"
	OperationTimelineKindDelivery = "delivery_attempt"
)

// Attempt outcomes shared by HTTP execution and workflow step attempts.
const (
	OperationAttemptRunning    = "running"
	OperationAttemptSucceeded  = "succeeded"
	OperationAttemptRetry      = "retry"
	OperationAttemptFailed     = "failed"
	OperationAttemptDeadLetter = "dead_letter"
	OperationAttemptCancelled  = "cancelled"
	OperationAttemptUnknown    = "unknown"
)

// OperationAttemptLogCorrelation names how a log window relates to its
// attempt: the lines its instance wrote while the attempt ran. Other requests
// served by the same instance at the same time interleave.
const OperationAttemptLogCorrelation = "instance_window"

type OperationTimelineResponse struct {
	OperationID string                   `json:"operation_id"`
	State       OperationState           `json:"state"`
	Generation  int                      `json:"generation"`
	Entries     []OperationTimelineEntry `json:"entries"`
	// EventsExpired reports that event history was pruned before the
	// Operation; attempts and deliveries are still returned.
	EventsExpired bool   `json:"events_expired,omitempty"`
	NextCursor    string `json:"next_cursor,omitempty"`
}

// OperationTimelineEntry carries exactly one of Event, Attempt or Delivery,
// selected by Kind. Attempt entries are snapshots at read time.
type OperationTimelineEntry struct {
	At       time.Time                 `json:"at"`
	Kind     string                    `json:"kind"`
	Event    *OperationEvent           `json:"event,omitempty"`
	Attempt  *OperationTimelineAttempt `json:"attempt,omitempty"`
	Delivery *OperationDeliveryAttempt `json:"delivery,omitempty"`
}

// OperationTimelineAttempt is one HTTP execution or workflow step attempt.
// Step attempts carry no generation: recovery resumes the same workflow run,
// whose step attempt numbers continue across generations.
type OperationTimelineAttempt struct {
	Generation    int                   `json:"generation,omitempty"`
	ExecutionID   string                `json:"execution_id"`
	Step          string                `json:"step,omitempty"`
	Attempt       int                   `json:"attempt"`
	Outcome       string                `json:"outcome"`
	HTTPStatus    *int                  `json:"http_status,omitempty"`
	Error         string                `json:"error,omitempty"`
	StartedAt     time.Time             `json:"started_at"`
	FinishedAt    *time.Time            `json:"finished_at,omitempty"`
	NextAttemptAt *time.Time            `json:"next_attempt_at,omitempty"`
	Logs          *OperationAttemptLogs `json:"logs,omitempty"`
}

// OperationAttemptLogs points an attempt at its instance's archived logs.
// Until is absent while the attempt runs. ArchiveDates are the UTC days the
// window touches, newest last.
type OperationAttemptLogs struct {
	InstanceID   string     `json:"instance_id"`
	Since        time.Time  `json:"since"`
	Until        *time.Time `json:"until,omitempty"`
	ArchiveDates []string   `json:"archive_dates"`
	Correlation  string     `json:"correlation"`
}

func (c *Client) GetOperationTimeline(ctx context.Context, slug, id string, limit int, cursor string) (OperationTimelineResponse, error) {
	var out OperationTimelineResponse
	query := url.Values{}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	path := operationAppPath(slug, id) + "/timeline"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
