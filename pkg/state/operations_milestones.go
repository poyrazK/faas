// ADR-715: stable milestone IDs cross execution generations, while publication remains fenced.
package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
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
	OperationID string
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
	for _, dependency := range canonical.DependsOn {
		if op.Subject != nil && dependency.SubjectType == op.Subject.Type && dependency.SubjectID == op.Subject.ID && dependency.Workflow == canonical.Workflow && dependency.InstanceID == canonical.InstanceID {
			return canonical, "", fmt.Errorf("%w: workflow cannot depend on itself", ErrInvalidArgument)
		}
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
	consumedInvariants := map[string]bool{}
	seen := make(map[string]bool, len(reports))
	for _, report := range reports {
		if seen[report.ID] {
			return ErrInvalidArgument
		}
		seen[report.ID] = true
		canonical, _, err := canonicalOperationWorkflowState(op, def, report)
		if err != nil {
			return err
		}
		report = canonical
		payloads := map[string][]byte{}
		for _, evidence := range report.EvidenceMilestones {
			milestone, exists := milestonesByID[evidence.ID]
			if !exists || milestone.Name != evidence.Name {
				return fmt.Errorf("%w: workflow state evidence must reference a milestone in the same transaction", ErrInvalidArgument)
			}
			payloads[evidence.ID] = milestone.Payload
		}
		for _, id := range requiredWorkflowEvidenceIDs(def, report) {
			if consumedInvariants[id] {
				return fmt.Errorf("%w: required business evidence cannot be reused across transition reports", ErrInvalidArgument)
			}
			consumedInvariants[id] = true
		}
		if err := validateWorkflowPolicyEvidence(def, report, payloads); err != nil {
			return err
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

type operationWorkflowStepDeclaration struct {
	Operation string
	Spec      api.OperationWorkflowSpec
	Active    bool
}

type operationWorkflowStepFactSummary struct {
	ContractVersion       int
	Step                  string
	Label                 string
	Operation             string
	Milestone             string
	Position              int
	MilestonesInRetention int64
	Latest                api.OperationWorkflowInstanceMilestoneRef
}

func addOperationWorkflowStepFactSummary(summaries map[int]map[string]*operationWorkflowStepFactSummary, milestone api.OperationMilestone, workflow, instanceID string) {
	for _, spec := range milestone.WorkflowSteps {
		if spec.Workflow != workflow || spec.InstanceID != instanceID {
			continue
		}
		version := effectiveWorkflowContractVersion(spec.Version)
		byStep := summaries[version]
		if byStep == nil {
			byStep = make(map[string]*operationWorkflowStepFactSummary)
			summaries[version] = byStep
		}
		summary := byStep[spec.Step]
		if summary == nil {
			summary = &operationWorkflowStepFactSummary{ContractVersion: version, Step: spec.Step, Label: spec.Label,
				Milestone: spec.Milestone, Position: spec.Position}
			byStep[spec.Step] = summary
		}
		summary.MilestonesInRetention++
		if summary.Latest.ID == "" || milestone.CreatedAt.After(summary.Latest.PublishedAt) ||
			milestone.CreatedAt.Equal(summary.Latest.PublishedAt) && (milestone.OperationID > summary.Latest.OperationID ||
				milestone.OperationID == summary.Latest.OperationID && milestone.ID > summary.Latest.ID) {
			summary.Latest = api.OperationWorkflowInstanceMilestoneRef{ID: milestone.ID, OperationID: milestone.OperationID,
				OccurredAt: milestone.OccurredAt, PublishedAt: milestone.CreatedAt}
		}
	}
}

func operationWorkflowStepFactSummaryList(summaries map[int]map[string]*operationWorkflowStepFactSummary) []operationWorkflowStepFactSummary {
	facts := make([]operationWorkflowStepFactSummary, 0)
	for _, byStep := range summaries {
		for _, summary := range byStep {
			facts = append(facts, *summary)
		}
	}
	sort.Slice(facts, func(i, j int) bool {
		if facts[i].ContractVersion != facts[j].ContractVersion {
			return facts[i].ContractVersion < facts[j].ContractVersion
		}
		return facts[i].Step < facts[j].Step
	})
	return facts
}

func operationWorkflowInstanceContractVersion(page api.OperationMilestonesResponse, workflow, instanceID string, declarations []operationWorkflowStepDeclaration, instanceState *api.OperationWorkflowState, retainedFacts []operationWorkflowStepFactSummary) int {
	state := instanceState
	if state == nil && len(page.WorkflowStates) > 0 {
		copy := page.WorkflowStates[0]
		state = &copy
	}
	if state != nil && state.ContractVersion > 0 {
		return effectiveWorkflowContractVersion(state.ContractVersion)
	}
	if n := len(page.WorkflowStateHistory); n > 0 {
		return effectiveWorkflowContractVersion(page.WorkflowStateHistory[n-1].ContractVersion)
	}
	var latest *operationWorkflowStepFactSummary
	for i := range retainedFacts {
		fact := &retainedFacts[i]
		if fact.Latest.ID == "" {
			continue
		}
		if latest == nil || fact.Latest.PublishedAt.After(latest.Latest.PublishedAt) ||
			fact.Latest.PublishedAt.Equal(latest.Latest.PublishedAt) && (fact.Latest.OperationID > latest.Latest.OperationID ||
				fact.Latest.OperationID == latest.Latest.OperationID && fact.Latest.ID > latest.Latest.ID) {
			latest = fact
		}
	}
	if latest != nil {
		return effectiveWorkflowContractVersion(latest.ContractVersion)
	}
	var latestMilestone *api.OperationMilestone
	var observedVersion int
	for i := range page.Milestones {
		milestone := &page.Milestones[i]
		for _, spec := range milestone.WorkflowSteps {
			if spec.Workflow != workflow || spec.InstanceID != instanceID {
				continue
			}
			if latestMilestone == nil || milestone.CreatedAt.After(latestMilestone.CreatedAt) ||
				milestone.CreatedAt.Equal(latestMilestone.CreatedAt) && (milestone.OperationID > latestMilestone.OperationID ||
					milestone.OperationID == latestMilestone.OperationID && milestone.ID > latestMilestone.ID) {
				latestMilestone = milestone
				observedVersion = effectiveWorkflowContractVersion(spec.Version)
			}
		}
	}
	if observedVersion > 0 {
		return observedVersion
	}
	for _, declaration := range declarations {
		if declaration.Active && declaration.Spec.Workflow == workflow && effectiveWorkflowContractVersion(declaration.Spec.Version) > observedVersion {
			observedVersion = effectiveWorkflowContractVersion(declaration.Spec.Version)
		}
	}
	if observedVersion == 0 {
		for _, declaration := range declarations {
			if declaration.Spec.Workflow == workflow && effectiveWorkflowContractVersion(declaration.Spec.Version) > observedVersion {
				observedVersion = effectiveWorkflowContractVersion(declaration.Spec.Version)
			}
		}
	}
	if observedVersion == 0 {
		return 1
	}
	return observedVersion
}

func projectOperationWorkflowInstance(page api.OperationMilestonesResponse, workflow, instanceID string, declarations []operationWorkflowStepDeclaration, instanceState *api.OperationWorkflowState, retainedFacts []operationWorkflowStepFactSummary) api.OperationMilestonesResponse {
	if workflow == "" || instanceID == "" {
		return page
	}
	version := operationWorkflowInstanceContractVersion(page, workflow, instanceID, declarations, instanceState, retainedFacts)
	state := instanceState
	if state == nil && len(page.WorkflowStates) > 0 {
		copy := page.WorkflowStates[0]
		state = &copy
	}

	stepsByName := make(map[string]api.OperationWorkflowInstanceStep)
	for _, declaration := range declarations {
		spec := declaration.Spec
		if spec.Workflow != workflow || effectiveWorkflowContractVersion(spec.Version) != version {
			continue
		}
		if _, exists := stepsByName[spec.Step]; exists {
			continue
		}
		stepsByName[spec.Step] = api.OperationWorkflowInstanceStep{Step: spec.Step, Label: spec.Label, Operation: declaration.Operation,
			Milestone: spec.Milestone, Position: spec.Position}
	}
	for _, milestone := range page.Milestones {
		for _, spec := range milestone.WorkflowSteps {
			if spec.Workflow != workflow || spec.InstanceID != instanceID || effectiveWorkflowContractVersion(spec.Version) != version {
				continue
			}
			step, exists := stepsByName[spec.Step]
			if !exists {
				step = api.OperationWorkflowInstanceStep{Step: spec.Step, Label: spec.Label, OperationID: milestone.OperationID,
					Milestone: spec.Milestone, Position: spec.Position}
			}
			step.Observed = true
			step.MilestonesInPage++
			if step.LatestMilestone == nil || milestone.CreatedAt.After(step.LatestMilestone.PublishedAt) ||
				milestone.CreatedAt.Equal(step.LatestMilestone.PublishedAt) && (milestone.OperationID > step.LatestMilestone.OperationID ||
					milestone.OperationID == step.LatestMilestone.OperationID && milestone.ID > step.LatestMilestone.ID) {
				step.LatestMilestone = &api.OperationWorkflowInstanceMilestoneRef{ID: milestone.ID, OperationID: milestone.OperationID,
					OccurredAt: milestone.OccurredAt, PublishedAt: milestone.CreatedAt}
			}
			stepsByName[spec.Step] = step
		}
	}
	for _, fact := range retainedFacts {
		if fact.ContractVersion != version {
			continue
		}
		step, exists := stepsByName[fact.Step]
		if !exists {
			step = api.OperationWorkflowInstanceStep{Step: fact.Step, Label: fact.Label, Operation: fact.Operation,
				Milestone: fact.Milestone, Position: fact.Position}
			if fact.Operation == "" {
				step.OperationID = fact.Latest.OperationID
			}
		}
		step.ObservedInRetention = fact.MilestonesInRetention > 0
		step.MilestonesInRetention = fact.MilestonesInRetention
		latest := fact.Latest
		if latest.ID != "" {
			step.LatestRetainedMilestone = &latest
		}
		stepsByName[fact.Step] = step
	}
	steps := make([]api.OperationWorkflowInstanceStep, 0, len(stepsByName))
	for _, step := range stepsByName {
		steps = append(steps, step)
	}
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].Position != steps[j].Position {
			return steps[i].Position < steps[j].Position
		}
		return steps[i].Step < steps[j].Step
	})
	allowedTransitions := make([]api.OperationWorkflowInstanceTransition, 0)
	transitionIndexes := make(map[string]struct{})
	for _, declaration := range declarations {
		spec := declaration.Spec
		if spec.Workflow != workflow || effectiveWorkflowContractVersion(spec.Version) != version {
			continue
		}
		for _, transition := range spec.Transitions {
			key := declaration.Operation + "\x00" + transition.From + "\x00" + transition.To
			if _, exists := transitionIndexes[key]; exists {
				// Multiple steps may bind to the same Operation and repeat its
				// operation-scoped copy of the workflow transitions.
				continue
			}
			transitionIndexes[key] = struct{}{}
			allowedTransitions = append(allowedTransitions, api.OperationWorkflowInstanceTransition{
				From: transition.From, To: transition.To, Operation: declaration.Operation,
				RequiredMilestones:          append([]string(nil), transition.RequiredMilestones...),
				RequiredEffects:             append([]api.OperationWorkflowEffectRequirement(nil), transition.RequiredEffects...),
				RequiredInvariants:          append([]api.OperationWorkflowInvariantRequirement(nil), transition.RequiredInvariants...),
				RequiredPolicies:            append([]api.OperationWorkflowPolicyRequirement(nil), transition.RequiredPolicies...),
				RequiredDependencyWorkflows: cloneTransitionDependencyWorkflows(transition.RequiredDependencyWorkflows),
			})
		}
	}
	sort.Slice(allowedTransitions, func(i, j int) bool {
		if allowedTransitions[i].From != allowedTransitions[j].From {
			return allowedTransitions[i].From < allowedTransitions[j].From
		}
		if allowedTransitions[i].To != allowedTransitions[j].To {
			return allowedTransitions[i].To < allowedTransitions[j].To
		}
		return allowedTransitions[i].Operation < allowedTransitions[j].Operation
	})
	page.WorkflowInstance = &api.OperationWorkflowInstanceSnapshot{Workflow: workflow, InstanceID: instanceID, ContractVersion: version,
		State: state, Steps: steps, AllowedTransitions: allowedTransitions,
		Decision:    explainOperationWorkflowDecision(state, allowedTransitions),
		Transitions: append(make([]api.OperationWorkflowStateHistoryEntry, 0, len(page.WorkflowStateHistory)), page.WorkflowStateHistory...),
		HasMore:     page.NextCursor != "" || page.NextWorkflowStateCursor != "", NextMilestoneCursor: page.NextCursor,
		NextTransitionCursor: page.NextWorkflowStateCursor}
	return page
}

func effectiveWorkflowContractVersion(version int) int {
	if version < 1 {
		return 1
	}
	return version
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

func explainOperationWorkflowDecision(state *api.OperationWorkflowState, edges []api.OperationWorkflowInstanceTransition) *api.OperationWorkflowDecision {
	d := &api.OperationWorkflowDecision{Reason: "state_unknown", Explanation: "No application state has been reported; next transitions cannot be determined.", NeedsAttention: true, NextActions: make([]api.OperationWorkflowInstanceTransition, 0), Blockers: make([]api.OperationWorkflowBlocker, 0)}
	if state == nil {
		return d
	}
	d.StateRevision = state.Revision
	d.Blockers = append(d.Blockers, state.Blockers...)
	d.NeedsAttention = state.Stale || state.Overdue || len(d.Blockers) > 0
	if state.Terminal {
		d.Reason, d.Explanation = "terminal", "The application reported a terminal state; no further transitions are proposed."
		if len(d.Blockers) > 0 {
			d.Explanation += " The application also reported blockers for the listed target Operations."
		}
		if state.Stale {
			d.Explanation += " The reported state has passed its declared stale threshold."
		}
		return d
	}
	for _, edge := range edges {
		if edge.From == state.State {
			d.NextActions = append(d.NextActions, edge)
		}
	}
	d.Reason, d.Explanation = "no_declared_transition", "No transition is declared from the reported state in the selected contract."
	if len(d.NextActions) > 0 {
		d.Reason, d.Explanation = "transitions_available", "These contract transitions match the reported state. Each required milestone must be committed with its transition."
	}
	if len(d.Blockers) > 0 {
		d.Reason = "application_blocked"
		d.Explanation += " The application reported blockers for the listed target Operations."
	}
	if state.Stale {
		d.Reason = "state_stale"
		d.Explanation += " The reported state has passed its declared stale threshold; verify the current business row before acting."
	}
	if state.Overdue {
		d.Reason = "deadline_overdue"
		d.Explanation += " The application-reported business deadline has passed; verify the current business row before acting."
	}
	return d
}

func workflowResolutionBlockerExists(blockers []api.OperationWorkflowBlocker, resolution api.OperationWorkflowBlockerResolution) bool {
	for _, blocker := range blockers {
		if blocker.Operation == resolution.Operation && blocker.Code == resolution.Code {
			return true
		}
	}
	return false
}

// Overdue is derived at read time; due dates are business observations, not authority.
func evaluateOperationWorkflowDeadline(at time.Time, state *api.OperationWorkflowState) {
	state.Overdue = false
	state.OverdueSeconds = 0
	if state.Terminal || state.DeadlineAt == "" {
		return
	}
	due, err := time.Parse(time.RFC3339Nano, state.DeadlineAt)
	if err != nil || at.Before(due) {
		return
	}
	state.Overdue = true
	seconds := at.Unix() - due.Unix()
	if at.Nanosecond() < due.Nanosecond() {
		seconds--
	}
	if seconds > 0 {
		state.OverdueSeconds = seconds
	}
}

func cloneTransitionDependencyWorkflows(input *[]string) *[]string {
	if input == nil {
		return nil
	}
	out := append([]string{}, (*input)...)
	return &out
}
