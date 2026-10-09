package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// WorkflowEventReplayPreviewOptions selects retained events by platform
// acceptance time, using the same bounded page shape as subscription preview.
type WorkflowEventReplayPreviewOptions struct {
	From  time.Time
	Until time.Time
	After string
	Limit int
}

func (o WorkflowEventReplayPreviewOptions) Validate() error {
	if o.From.IsZero() || o.Until.IsZero() || !o.From.Before(o.Until) {
		return fmt.Errorf("from and until must define a nonempty acceptance-time range")
	}
	if o.Limit < 0 || o.Limit > EventReplayPreviewPageMax {
		return fmt.Errorf("limit must be between 1 and %d when supplied", EventReplayPreviewPageMax)
	}
	if len(o.After) > EventReplayPreviewCursorMaxBytes {
		return fmt.Errorf("after must be at most %d bytes", EventReplayPreviewCursorMaxBytes)
	}
	return nil
}

// WorkflowEventReplayPreviewMatch exposes only retained event metadata and
// admission state. It never includes the event payload or workflow definition.
type WorkflowEventReplayPreviewMatch struct {
	EventID           string    `json:"event_id"`
	EventSource       string    `json:"event_source"`
	EventType         string    `json:"event_type"`
	SchemaVersion     string    `json:"schema_version,omitempty"`
	AcceptedAt        time.Time `json:"accepted_at"`
	OriginalRecipient string    `json:"original_recipient"` // always captured for returned matches
	RoutingState      string    `json:"routing_state"`
	FilterMatched     bool      `json:"filter_matched"`
	AdmissionRecorded bool      `json:"admission_recorded"`
	WorkflowRunID     string    `json:"workflow_run_id,omitempty"`
	WorkflowRunStatus string    `json:"workflow_run_status,omitempty"`
	ReceiptURL        string    `json:"receipt_url"`
}

// WorkflowEventReplayPreviewResponse is a bounded read-only view over retained
// envelopes and their immutable workflow recipient snapshots.
type WorkflowEventReplayPreviewResponse struct {
	AppSlug                 string                            `json:"app_slug"`
	WorkflowName            string                            `json:"workflow_name"`
	From                    time.Time                         `json:"from"`
	Until                   time.Time                         `json:"until"`
	CutoffAt                time.Time                         `json:"cutoff_at"`
	ObservedAt              time.Time                         `json:"observed_at"`
	Coverage                string                            `json:"coverage"`
	Retention               EventReplayPreviewRetention       `json:"retention"`
	ScannedCount            int                               `json:"scanned_count"`
	CapturedCount           int                               `json:"captured_count"`
	MatchedCount            int                               `json:"matched_count"`
	FilterMismatchCount     int                               `json:"filter_mismatch_count"`
	NotCapturedCount        int                               `json:"not_captured_count"`
	UnknownRecipientCount   int                               `json:"unknown_recipient_count"`
	AlreadyAdmittedCount    int                               `json:"already_admitted_count"`
	PotentialAdmissionCount int                               `json:"potential_admission_count"`
	Matches                 []WorkflowEventReplayPreviewMatch `json:"matches"`
	NextAfter               string                            `json:"next_after,omitempty"`
}

// PreviewWorkflowEventReplay inspects a workflow's captured historical event
// recipients without admitting runs or changing delivery state.
func (c *Client) PreviewWorkflowEventReplay(ctx context.Context, appSlug, workflowName string, options WorkflowEventReplayPreviewOptions) (WorkflowEventReplayPreviewResponse, error) {
	q := url.Values{
		"workflow_name": {workflowName},
		"from":          {options.From.UTC().Format(time.RFC3339Nano)},
		"until":         {options.Until.UTC().Format(time.RFC3339Nano)},
	}
	if options.After != "" {
		q.Set("after", options.After)
	}
	if options.Limit != 0 {
		q.Set("limit", strconv.Itoa(options.Limit))
	}
	var out WorkflowEventReplayPreviewResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(appSlug)+"/workflow-event-replay-preview?"+q.Encode(), nil, &out)
	return out, err
}
