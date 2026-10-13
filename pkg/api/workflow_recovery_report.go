package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type WorkflowRecoveryReportOptions struct {
	Workflow      string
	PolicyVersion int64
	CreatedAfter  time.Time
	CreatedBefore time.Time
}

func (o WorkflowRecoveryReportOptions) HistoryOptions() WorkflowRecoveryHistoryOptions {
	return WorkflowRecoveryHistoryOptions{Workflow: o.Workflow, PolicyVersion: o.PolicyVersion, CreatedAfter: o.CreatedAfter, CreatedBefore: o.CreatedBefore}
}
func (o WorkflowRecoveryReportOptions) Validate() error { return o.HistoryOptions().Validate() }

type WorkflowRecoveryReportTotals struct {
	Attempts               int      `json:"attempts"`
	Succeeded              int      `json:"succeeded"`
	Failed                 int      `json:"failed"`
	Cancelled              int      `json:"cancelled"`
	OtherResults           int      `json:"other_results"`
	WithoutObservedResult  int      `json:"without_observed_result"`
	Pauses                 int      `json:"pauses"`
	TimedResults           int      `json:"timed_results"`
	MeanRetryResultSeconds *float64 `json:"mean_retry_result_seconds,omitempty"`
}
type WorkflowRecoveryReportBlocker struct {
	Reason   string                         `json:"reason"`
	Actions  int                            `json:"actions"`
	Evidence []WorkflowRecoveryHistoryEntry `json:"evidence"`
}
type WorkflowRecoveryReportGroup struct {
	Workflow      string                       `json:"workflow"`
	PolicyVersion int64                        `json:"policy_version"`
	Totals        WorkflowRecoveryReportTotals `json:"totals"`
}
type WorkflowRecoveryReport struct {
	ObservedAt     time.Time                       `json:"observed_at"`
	CreatedAfter   time.Time                       `json:"created_after"`
	CreatedBefore  time.Time                       `json:"created_before"`
	ScannedEntries int                             `json:"scanned_entries"`
	DurableEntries int                             `json:"durable_entries"`
	Truncated      bool                            `json:"truncated"`
	CoverageNotes  []string                        `json:"coverage_notes"`
	Totals         WorkflowRecoveryReportTotals    `json:"totals"`
	Groups         []WorkflowRecoveryReportGroup   `json:"groups"`
	Blockers       []WorkflowRecoveryReportBlocker `json:"blockers"`
	Evidence       []WorkflowRecoveryHistoryEntry  `json:"evidence"`
}

func (c *Client) GetWorkflowRecoveryReport(ctx context.Context, app string, o WorkflowRecoveryReportOptions) (WorkflowRecoveryReport, error) {
	var out WorkflowRecoveryReport
	if err := o.Validate(); err != nil {
		return out, err
	}
	q := url.Values{}
	if o.Workflow != "" {
		q.Set("workflow", o.Workflow)
	}
	if o.PolicyVersion != 0 {
		q.Set("policy_version", strconv.FormatInt(o.PolicyVersion, 10))
	}
	if !o.CreatedAfter.IsZero() {
		q.Set("created_after", o.CreatedAfter.Format(time.RFC3339Nano))
	}
	if !o.CreatedBefore.IsZero() {
		q.Set("created_before", o.CreatedBefore.Format(time.RFC3339Nano))
	}
	err := c.do(ctx, http.MethodGet, recoveryPolicyPath(app)+"/report?"+q.Encode(), nil, &out)
	return out, err
}
