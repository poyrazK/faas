// ADR-715: stable milestone IDs cross execution generations, while publication remains fenced.
package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

type OperationMilestoneStore interface {
	ValidateOperationMilestones(context.Context, string, OperationExecutionAuthority, []api.OperationMilestoneRequest) error
	ReportOperationMilestone(context.Context, string, OperationExecutionAuthority, api.OperationMilestoneRequest) (api.OperationMilestone, error)
	ValidateOperationWorkflowStates(context.Context, string, OperationExecutionAuthority, []api.OperationWorkflowStateReport, []api.OperationMilestoneRequest) error
	ReportOperationWorkflowState(context.Context, string, OperationExecutionAuthority, api.OperationWorkflowStateReport) (api.OperationWorkflowStateReportResponse, error)
	ListPlatformTenantOperationMilestones(context.Context, string, string, api.OperationMilestoneListOptions) (api.OperationMilestonesResponse, error)
	ListAccountOperationMilestones(context.Context, string, api.OperationMilestoneListOptions) (api.OperationMilestonesResponse, error)
}

type operationMilestoneReceipt struct {
	Milestone   api.OperationMilestone
	Fingerprint string
}

type operationWorkflowStateReceipt struct {
	Response    api.OperationWorkflowStateReportResponse
	Fingerprint string
	History     api.OperationWorkflowStateHistoryEntry
}

type operationWorkflowStateRecord struct {
	State       api.OperationWorkflowState
	ReportID    string
	AccountID   string
	AppID       string
	TenantID    string
	Scope       string
	SubjectType string
	SubjectID   string
}

func operationWorkflowStateIsStale(now time.Time, workflowState api.OperationWorkflowState) bool {
	if workflowState.Terminal || workflowState.StaleAfterSeconds < 1 || workflowState.OccurredAt.IsZero() {
		return false
	}
	deadline := workflowState.OccurredAt.Add(time.Duration(workflowState.StaleAfterSeconds) * time.Second)
	return !deadline.After(now)
}

func operationWorkflowStateKey(account, app, tenant, scope, subjectType, subjectID, workflow, instanceID string) string {
	encoded, _ := json.Marshal([]string{account, app, tenant, scope, subjectType, subjectID, workflow, instanceID})
	return string(encoded)
}

func canonicalOperationMilestone(op Operation, def OperationDefinition, report api.OperationMilestoneRequest) (api.OperationMilestoneRequest, string, error) {
	contract, err := operations.Compile(def.Spec, op.PlanLimits)
	if err != nil {
		return report, "", fmt.Errorf("state: compile milestone contract: %w", err)
	}
	report, err = contract.CanonicalMilestone(report)
	if err != nil {
		return report, "", fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	raw, _ := json.Marshal(report)
	fingerprint, err := operations.InputFingerprint(raw)
	return report, fingerprint, err
}

func validateOperationMilestoneBatch(op Operation, def OperationDefinition, reports []api.OperationMilestoneRequest) error {
	raw, err := json.Marshal(api.OperationMilestoneValidationRequest{Milestones: reports})
	if err != nil || len(reports) == 0 || len(reports) > api.OperationMilestonesMaxPerOperation || len(raw) > api.OperationMilestoneBatchMaxBytes {
		return ErrInvalidArgument
	}
	seen := make(map[string]bool, len(reports))
	for _, report := range reports {
		if seen[report.ID] {
			return ErrInvalidArgument
		}
		seen[report.ID] = true
		if _, _, err := canonicalOperationMilestone(op, def, report); err != nil {
			return err
		}
	}
	return nil
}

func canonicalOperationWorkflowState(op Operation, def OperationDefinition, report api.OperationWorkflowStateReport) (api.OperationWorkflowStateReport, string, error) {
	contract, err := operations.Compile(def.Spec, op.PlanLimits)
	if err != nil {
		return report, "", fmt.Errorf("state: compile workflow state contract: %w", err)
	}
	canonical, fingerprint, err := contract.CanonicalWorkflowState(op.Subject != nil, report)
	if err != nil {
		return canonical, "", fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	return canonical, fingerprint, nil
}

func validateOperationWorkflowStateBatch(op Operation, def OperationDefinition, reports []api.OperationWorkflowStateReport, milestones []api.OperationMilestoneRequest) error {
	raw, err := json.Marshal(api.OperationWorkflowStateValidationRequest{WorkflowStates: reports, Milestones: milestones})
	if err != nil || len(reports) == 0 || len(reports) > api.OperationWorkflowStateReportsMaxPerTransaction || len(milestones) > api.OperationMilestonesMaxPerOperation || len(raw) > api.OperationWorkflowStateBatchMaxBytes {
		return ErrInvalidArgument
	}
	milestonesByID := make(map[string]api.OperationMilestoneRequest, len(milestones))
	if len(milestones) > 0 {
		if err := validateOperationMilestoneBatch(op, def, milestones); err != nil {
			return err
		}
		for _, milestone := range milestones {
			milestonesByID[milestone.ID] = milestone
		}
	}
	seen := make(map[string]bool, len(reports))
	for _, report := range reports {
		if seen[report.ID] {
			return ErrInvalidArgument
		}
		seen[report.ID] = true
		if _, _, err := canonicalOperationWorkflowState(op, def, report); err != nil {
			return err
		}
		for _, evidence := range report.EvidenceMilestones {
			milestone, exists := milestonesByID[evidence.ID]
			if !exists || milestone.Name != evidence.Name {
				return fmt.Errorf("%w: workflow state evidence must reference a milestone in the same transaction", ErrInvalidArgument)
			}
		}
	}
	return nil
}

func validatePublishedWorkflowEvidence(report api.OperationWorkflowStateReport, milestoneNames map[string]string) error {
	for _, evidence := range report.EvidenceMilestones {
		if name, exists := milestoneNames[evidence.ID]; !exists || name != evidence.Name {
			return fmt.Errorf("%w: workflow state evidence references an unpublished milestone", ErrInvalidArgument)
		}
	}
	return nil
}

func newOperationMilestone(op *Operation, inv Invocation, def OperationDefinition, report api.OperationMilestoneRequest, now time.Time) (api.OperationMilestone, api.OperationEvent, error) {
	if op.MilestoneCount >= api.OperationMilestonesMaxPerOperation {
		return api.OperationMilestone{}, api.OperationEvent{}, NewOperationLimitError("milestones_per_operation", api.OperationMilestonesMaxPerOperation, int64(op.MilestoneCount)+1)
	}
	now = now.UTC().Truncate(time.Microsecond)
	workflowSteps, err := operations.WorkflowStepsForMilestone(def.Spec, report.Name, report.Payload)
	if err != nil {
		return api.OperationMilestone{}, api.OperationEvent{}, fmt.Errorf("state: resolve workflow instance: %w", err)
	}
	milestone := api.OperationMilestone{ID: report.ID, OperationID: op.ID, Name: report.Name, Payload: report.Payload, OccurredAt: report.OccurredAt, CreatedAt: now, Sequence: op.LatestSequence + 1,
		WorkflowSteps: workflowSteps}
	if op.Subject != nil {
		subject := *op.Subject
		milestone.Subject = &subject
	}
	op.MilestoneCount++
	// The generic stream carries a notice; the bounded JSON payload lives in
	// the dedicated retained ledger, preserving its numeric/string encoding.
	notice := struct {
		ID         string    `json:"id"`
		Name       string    `json:"name"`
		OccurredAt time.Time `json:"occurred_at"`
		Sequence   int64     `json:"sequence"`
	}{milestone.ID, milestone.Name, milestone.OccurredAt, milestone.Sequence}
	event := operationEvent(op, inv, "milestone", notice, now)
	return milestone, event, nil
}

func cloneOperationMilestone(milestone api.OperationMilestone) api.OperationMilestone {
	encoded, _ := json.Marshal(milestone)
	var copy api.OperationMilestone
	_ = json.Unmarshal(encoded, &copy)
	return copy
}

type operationMilestoneCursor struct {
	Version     int       `json:"v"`
	Query       string    `json:"query"`
	CreatedAt   time.Time `json:"created_at"`
	OperationID string    `json:"operation_id"`
	ID          string    `json:"id"`
}

type operationWorkflowStateHistoryCursor struct {
	Version     int       `json:"v"`
	Query       string    `json:"query"`
	Revision    int64     `json:"revision"`
	PublishedAt time.Time `json:"published_at"`
	OperationID string    `json:"operation_id"`
	ID          string    `json:"id"`
}

func prepareOperationMilestoneHistory(account, tenant string, opts api.OperationMilestoneListOptions, operator bool) (api.OperationMilestoneListOptions, operationMilestoneCursor, error) {
	history, _, err := prepareOperationHistoryQuery(account, tenant, api.OperationListOptions{AppID: opts.AppID, Scope: opts.Scope, TenantID: opts.TenantID, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, Limit: opts.Limit}, operator)
	if err != nil {
		return opts, operationMilestoneCursor{}, err
	}
	opts.AppID, opts.TenantID, opts.Limit = history.AppID, history.TenantID, history.Limit
	account = uuid.MustParse(account).String()
	if tenant != "" {
		tenant = uuid.MustParse(tenant).String()
	}
	if len(opts.Cursor) > api.OperationHistoryCursorMaxBytes || len(opts.WorkflowStateCursor) > api.OperationHistoryCursorMaxBytes ||
		opts.OperationID != "" && opts.SubjectType != "" || opts.OperationID == "" && opts.SubjectType == "" {
		return opts, operationMilestoneCursor{}, ErrInvalidArgument
	}
	if opts.WorkflowStaleOnly && (opts.SubjectType == "" || opts.OperationID != "") {
		return opts, operationMilestoneCursor{}, ErrInvalidArgument
	}
	if (opts.Workflow == "") != (opts.WorkflowInstanceID == "") || opts.Workflow != "" &&
		(opts.SubjectType == "" || opts.OperationID != "" || api.ValidateOperationWorkflowName(opts.Workflow) != nil || api.ValidateOperationWorkflowInstanceID(opts.WorkflowInstanceID) != nil) {
		return opts, operationMilestoneCursor{}, ErrInvalidArgument
	}
	if opts.WorkflowStateCursor != "" && opts.Workflow == "" {
		return opts, operationMilestoneCursor{}, ErrInvalidArgument
	}
	if opts.OperationID != "" {
		id, err := uuid.Parse(opts.OperationID)
		if err != nil || id == uuid.Nil {
			return opts, operationMilestoneCursor{}, ErrInvalidArgument
		}
		opts.OperationID = id.String()
	}
	selectors := []any{"milestones-v1", account, tenant, operator, opts.AppID, opts.Scope, opts.OperationID, opts.SubjectType, opts.SubjectID}
	if opts.Workflow != "" {
		selectors = append(selectors, "workflow-instance-v1", opts.Workflow, opts.WorkflowInstanceID)
	}
	raw, _ := json.Marshal(selectors)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	cursor := operationMilestoneCursor{Version: 1, Query: digest}
	if opts.Cursor == "" {
		return opts, cursor, nil
	}
	raw, err = base64.RawURLEncoding.Strict().DecodeString(opts.Cursor)
	if err != nil {
		return opts, cursor, ErrInvalidArgument
	}
	raw, err = operations.CanonicalJSON(raw)
	if err != nil {
		return opts, cursor, ErrInvalidArgument
	}
	cursor = operationMilestoneCursor{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.Query != digest || cursor.CreatedAt.IsZero() || !cursor.CreatedAt.Equal(cursor.CreatedAt.Truncate(time.Microsecond)) {
		return opts, cursor, ErrInvalidArgument
	}
	for _, value := range []string{cursor.ID, cursor.OperationID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return opts, cursor, ErrInvalidArgument
		}
	}
	return opts, cursor, nil
}

func prepareOperationWorkflowStateHistoryCursor(account, tenant string, opts api.OperationMilestoneListOptions, operator bool) (operationWorkflowStateHistoryCursor, error) {
	if opts.Workflow == "" {
		if opts.WorkflowStateCursor != "" {
			return operationWorkflowStateHistoryCursor{}, ErrInvalidArgument
		}
		return operationWorkflowStateHistoryCursor{}, nil
	}
	if tenant != "" {
		tenant = uuid.MustParse(tenant).String()
	}
	selectors := []any{"workflow-state-history-v1", uuid.MustParse(account).String(), tenant, operator,
		opts.AppID, opts.TenantID, opts.Scope, opts.SubjectType, opts.SubjectID, opts.Workflow, opts.WorkflowInstanceID}
	raw, _ := json.Marshal(selectors)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	cursor := operationWorkflowStateHistoryCursor{Version: 1, Query: digest}
	if opts.WorkflowStateCursor == "" {
		return cursor, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(opts.WorkflowStateCursor)
	if err != nil {
		return cursor, ErrInvalidArgument
	}
	raw, err = operations.CanonicalJSON(raw)
	if err != nil {
		return cursor, ErrInvalidArgument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.Query != digest || cursor.Revision < 1 ||
		cursor.PublishedAt.IsZero() || !cursor.PublishedAt.Equal(cursor.PublishedAt.Truncate(time.Microsecond)) {
		return operationWorkflowStateHistoryCursor{}, ErrInvalidArgument
	}
	for _, value := range []string{cursor.ID, cursor.OperationID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return operationWorkflowStateHistoryCursor{}, ErrInvalidArgument
		}
	}
	return cursor, nil
}

func workflowStateHistoryAfterCursor(row api.OperationWorkflowStateHistoryEntry, cursor operationWorkflowStateHistoryCursor) bool {
	if cursor.ID == "" {
		return true
	}
	if row.Revision != cursor.Revision {
		return row.Revision > cursor.Revision
	}
	if !row.PublishedAt.Equal(cursor.PublishedAt) {
		return row.PublishedAt.After(cursor.PublishedAt)
	}
	if row.OperationID != cursor.OperationID {
		return row.OperationID > cursor.OperationID
	}
	return row.ID > cursor.ID
}

func operationWorkflowStateHistoryPage(rows []api.OperationWorkflowStateHistoryEntry, limit int, cursor operationWorkflowStateHistoryCursor) ([]api.OperationWorkflowStateHistoryEntry, string) {
	if len(rows) <= limit {
		return rows, ""
	}
	page := rows[:limit]
	last := page[len(page)-1]
	cursor.Revision, cursor.PublishedAt, cursor.OperationID, cursor.ID = last.Revision, last.PublishedAt, last.OperationID, last.ID
	raw, _ := json.Marshal(cursor)
	return page, base64.RawURLEncoding.EncodeToString(raw)
}

func operationMilestonePage(rows []api.OperationMilestone, limit int, cursor operationMilestoneCursor) api.OperationMilestonesResponse {
	page := api.OperationMilestonesResponse{Milestones: rows}
	if len(rows) > limit {
		page.Milestones = rows[:limit]
		last := rows[limit-1]
		cursor.CreatedAt, cursor.OperationID, cursor.ID = last.CreatedAt, last.OperationID, last.ID
		raw, _ := json.Marshal(cursor)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page
}

func operationMilestoneMatchesWorkflow(milestone api.OperationMilestone, workflow, instanceID string) bool {
	for _, step := range milestone.WorkflowSteps {
		if step.Workflow == workflow && step.InstanceID == instanceID {
			return true
		}
	}
	return false
}

func filterOperationWorkflowSteps(steps []api.OperationWorkflowSpec, workflow, instanceID string) []api.OperationWorkflowSpec {
	filtered := make([]api.OperationWorkflowSpec, 0, len(steps))
	for _, step := range steps {
		if step.Workflow == workflow && step.InstanceID == instanceID {
			filtered = append(filtered, step)
		}
	}
	return filtered
}
