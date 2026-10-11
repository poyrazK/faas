package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowDiagnosticsTenantReadsAndCohortChanges(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, tenant, otherTenant, app := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	start := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Microsecond)
	// Seed retained reports, as loaded from durable storage, to exercise the read
	// projections independently of report validation and publication.
	seed := func(owner, customer, application, scope, instance string, expired bool) {
		data := m.operationMemoryLocked()
		opID, defID := uuid.NewString(), uuid.NewString()
		o := diagnosticsHistory(1, "restoring", "", start)
		o.History.OperationID, o.History.InstanceID = opID, instance
		o.History.Blockers = []api.OperationWorkflowBlocker{{Operation: "restore", Code: "provider-wait", Owner: "provider"}}
		current := diagnosticsCurrent(o)
		current.Blockers = o.History.Blockers
		subject := api.OperationSubject{Type: "database", ID: "db-" + instance}
		data.operations[opID] = Operation{AccountID: owner, AppID: application, Scope: scope, PlatformTenantID: customer, DefinitionID: defID,
			OperationResponse: api.OperationResponse{ID: opID, State: api.OperationRunning, Subject: &subject}}
		if expired {
			op := data.operations[opID]
			op.State, op.ExpiresAt = api.OperationSucceeded, start
			data.operations[opID] = op
		}
		data.definitions[defID] = OperationDefinition{AccountID: owner, OperationDefinitionResponse: api.OperationDefinitionResponse{
			AppID: application, Scope: scope, Spec: api.OperationDefinitionSpec{Name: "restore", WorkflowSteps: []api.OperationWorkflowSpec{{Workflow: "recovery", Version: 1, States: []string{"restoring", "ready"}, TerminalStates: []string{"ready"}, StateSLABudgetSeconds: map[string]int64{"restoring": 60}, StateSLAWarningPercent: map[string]int64{"restoring": 80}}}}}}
		data.workflowStateReports[opID+"/"+o.History.ID] = operationWorkflowStateReceipt{History: o.History}
		data.workflowStates[instance] = operationWorkflowStateRecord{AccountID: owner, AppID: application, TenantID: customer, Scope: scope, SubjectType: subject.Type, SubjectID: subject.ID, OperationID: opID, ReportID: o.History.ID, State: current}
	}
	seed(account, tenant, app, "prod", "first", false)
	seed(account, otherTenant, app, "prod", "second", false)
	seed(account, tenant, app, "prod", "expired", true)
	seed(uuid.NewString(), tenant, app, "prod", "foreign-account", false)
	seed(account, tenant, uuid.NewString(), "prod", "foreign-app", false)
	seed(account, tenant, app, "preview", "foreign-scope", false)
	opts := api.OperationWorkflowAttentionOptions{AppID: app, Scope: "prod", Workflow: "recovery", Owner: "provider", Reason: "blocked"}
	page, err := m.ListPlatformTenantWorkflowAttention(ctx, account, tenant, opts)
	if err != nil || len(page.Items) != 1 || page.Items[0].State.InstanceID != "first" || page.Items[0].PlatformTenantID != "" {
		t.Fatalf("tenant diagnostic leaked unrelated or expired work: %+v %v", page, err)
	}
	opts.Limit = 1
	operatorPage, err := m.ListAccountWorkflowAttention(ctx, account, opts)
	if err != nil || len(operatorPage.Items) != 1 || operatorPage.NextCursor == "" || operatorPage.Items[0].PlatformTenantID == "" {
		t.Fatalf("operator pagination=%+v %v", operatorPage, err)
	}
	opts.Cursor = operatorPage.NextCursor
	next, err := m.ListAccountWorkflowAttention(ctx, account, opts)
	if err != nil || len(next.Items) != 1 || next.Items[0].State.InstanceID == operatorPage.Items[0].State.InstanceID || next.NextCursor != "" {
		t.Fatalf("duplicate or missing continuation: %+v %v", next, err)
	}
	opts.Cursor, opts.Owner = "", "unrelated"
	if filtered, err := m.ListAccountWorkflowAttention(ctx, account, opts); err != nil || len(filtered.Items) != 0 {
		t.Fatalf("owner filter ignored: %+v %v", filtered, err)
	}
	performanceOpts := api.OperationWorkflowPerformanceOptions{AppID: app, Scope: "prod", Workflow: "recovery"}
	summary, err := m.SummarizePlatformTenantWorkflowPerformance(ctx, account, tenant, performanceOpts)
	if err != nil || summary.Ongoing.CompleteHistoryWorkflowCount != 1 || summary.Ongoing.SLAEvaluatedWorkflowCount != 1 || summary.CohortToken == "" {
		t.Fatalf("tenant cohort=%+v %v", summary, err)
	}
	rankedOpts := api.OperationWorkflowPerformanceInstanceOptions{OperationWorkflowPerformanceOptions: performanceOpts, OperationWorkflowPerformanceGroup: api.OperationWorkflowPerformanceGroup{Dimension: "blocked_time"}, Cohort: "ongoing", CohortToken: summary.CohortToken}
	ranked, err := m.ListPlatformTenantWorkflowPerformanceInstances(ctx, account, tenant, rankedOpts)
	if err != nil || len(ranked.Items) != 1 || ranked.Items[0].State.InstanceID != "first" || ranked.Items[0].PlatformTenantID != "" || ranked.Items[0].ObservedSeconds != summary.Ongoing.BlockedTime.TotalSeconds || !ranked.EvaluatedAt.Equal(summary.EvaluatedAt) {
		t.Fatalf("ranked detail disagreed with frozen summary: %+v %v", ranked, err)
	}
	if _, err := m.ListPlatformTenantWorkflowPerformanceInstances(ctx, account, otherTenant, rankedOpts); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("cohort token crossed tenant boundary", err)
	}
	operatorSummary, err := m.SummarizeAccountWorkflowPerformance(ctx, account, performanceOpts)
	if err != nil || operatorSummary.Ongoing.CompleteHistoryWorkflowCount != 2 {
		t.Fatalf("operator cohort=%+v %v", operatorSummary, err)
	}
	rankedOpts.CohortToken = operatorSummary.CohortToken
	operatorRanked, err := m.ListAccountWorkflowPerformanceInstances(ctx, account, rankedOpts)
	if err != nil || len(operatorRanked.Items) != 2 || operatorRanked.Items[0].PlatformTenantID == "" || operatorRanked.Items[1].PlatformTenantID == "" {
		t.Fatalf("operator detail=%+v %v", operatorRanked, err)
	}
	// A retained report changes after the summary. The old token must require a
	// refresh, rather than silently ranking a different cohort under old totals.
	data := m.operationMemoryLocked()
	first := data.workflowStates["first"]
	first.State.Revision++
	data.workflowStates["first"] = first
	rankedOpts.CohortToken = summary.CohortToken
	if _, err := m.ListPlatformTenantWorkflowPerformanceInstances(ctx, account, tenant, rankedOpts); !errors.Is(err, ErrWorkflowPerformanceCohortChanged) {
		t.Fatal("changed cohort silently reused stale token", err)
	}
	refreshed, err := m.SummarizePlatformTenantWorkflowPerformance(ctx, account, tenant, performanceOpts)
	if err != nil || refreshed.Ongoing.CompleteHistoryWorkflowCount != 0 || refreshed.Ongoing.ExcludedIncompleteWorkflowCount != 1 || refreshed.Ongoing.SLAUnknownWorkflowCount != 1 {
		t.Fatalf("unpublished latest revision invented performance: %+v %v", refreshed, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.SummarizeAccountWorkflowPerformance(cancelled, account, performanceOpts); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled diagnostic read was accepted", err)
	}
}

func diagnosticsHistory(revision int64, state, from string, at time.Time) workflowBottleneckObservation {
	return workflowBottleneckObservation{History: api.OperationWorkflowStateHistoryEntry{
		ID: fmt.Sprintf("report-%d", revision), OperationID: "restore-operation", Workflow: "recovery", InstanceID: "restore-1",
		Revision: revision, ContractVersion: 1, State: state, FromState: from, OccurredAt: at, PublishedAt: at,
	}}
}

func diagnosticsCurrent(o workflowBottleneckObservation) api.OperationWorkflowState {
	h := o.History
	return api.OperationWorkflowState{OperationID: h.OperationID, ReportID: h.ID, Workflow: h.Workflow, InstanceID: h.InstanceID,
		Revision: h.Revision, ContractVersion: h.ContractVersion, State: h.State, Terminal: o.Terminal, UpdatedAt: h.OccurredAt}
}

func TestWorkflowDiagnosticsObservedRecoveryIntervals(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := diagnosticsHistory(1, "restoring", "", start)
	first.History.Blockers = []api.OperationWorkflowBlocker{{Operation: "restore", Code: "provider-wait", Owner: "provider"}, {Operation: "restore", Code: "approval", Owner: "operator"}}
	second := diagnosticsHistory(2, "verifying", "restoring", start.Add(10*time.Second))
	second.History.Blockers = []api.OperationWorkflowBlocker{{Operation: "verify", Code: "probe", Owner: "operator"}}
	current := diagnosticsCurrent(second)
	verifiedAt := start.Add(15 * time.Second)
	verifications := []api.OperationWorkflowResolutionVerification{
		{ResolutionOperationID: first.History.OperationID, ResolutionReportID: first.History.ID, Status: "verified", VerifiedAt: &verifiedAt, Resolution: api.OperationWorkflowBlockerResolution{VerificationOwner: "operator"}},
		{ResolutionOperationID: second.History.OperationID, ResolutionReportID: second.History.ID, Status: "awaiting_verification", Resolution: api.OperationWorkflowBlockerResolution{VerificationOwner: "provider"}},
	}
	b := workflowBottlenecks([]workflowBottleneckObservation{second, first}, &current, verifications, start.Add(20*time.Second))
	if !b.HistoryComplete || !b.Ongoing || b.StateSeconds != 20 || b.BlockedSeconds != 20 || b.VerificationWaitSeconds != 25 {
		t.Fatalf("observed intervals must count wall time once and each verification separately: %+v", b)
	}
	if len(b.States) != 2 || len(b.Blockers) != 3 || len(b.VerificationOwners) != 2 || b.VerificationOwners[0].Owner != "operator" || b.VerificationOwners[1].PendingCount != 1 {
		t.Fatalf("lost attribution or verification obligation: %+v", b)
	}
	for _, blocker := range b.Blockers {
		if blocker.ObservedSeconds != 10 {
			t.Fatalf("blocker duration=%+v", blocker)
		}
	}
	terminal := diagnosticsHistory(3, "ready", "verifying", start.Add(20*time.Second))
	terminal.Terminal = true
	completed := diagnosticsCurrent(terminal)
	b = workflowBottlenecks([]workflowBottleneckObservation{first, second, terminal}, &completed, nil, start.Add(time.Hour))
	if !b.HistoryComplete || b.Ongoing || b.StateSeconds != 20 || b.BlockedSeconds != 20 {
		t.Fatalf("completed recovery must not accrue time after terminal evidence: %+v", b)
	}

	for _, tc := range []struct {
		name, reason string
		mutate       func(*workflowBottleneckObservation, *workflowBottleneckObservation, *api.OperationWorkflowState)
	}{
		{"gap", "revision_gap", func(a, b *workflowBottleneckObservation, c *api.OperationWorkflowState) {
			b.History.Revision = 3
			c.Revision = 3
		}},
		{"duplicate", "duplicate_revision", func(a, b *workflowBottleneckObservation, c *api.OperationWorkflowState) {
			b.History.Revision = 1
			c.Revision = 1
		}},
		{"contract", "contract_changed", func(a, b *workflowBottleneckObservation, c *api.OperationWorkflowState) {
			b.History.ContractVersion = 2
			c.ContractVersion = 2
		}},
		{"discontinuity", "state_discontinuity", func(a, b *workflowBottleneckObservation, c *api.OperationWorkflowState) {
			b.History.FromState = "unobserved"
		}},
		{"reversed time", "out_of_order_time", func(a, b *workflowBottleneckObservation, c *api.OperationWorkflowState) {
			b.History.OccurredAt = start.Add(-time.Second)
		}},
		{"future", "future_observation", func(a, b *workflowBottleneckObservation, c *api.OperationWorkflowState) {
			b.History.OccurredAt = start.Add(time.Hour)
		}},
		{"missing start", "missing_start", func(a, b *workflowBottleneckObservation, c *api.OperationWorkflowState) {
			a.History.Revision = 2
			b.History.Revision = 3
			c.Revision = 3
		}},
		{"missing latest", "missing_latest", func(a, b *workflowBottleneckObservation, c *api.OperationWorkflowState) { c.ReportID = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, next, c := first, second, current
			tc.mutate(&a, &next, &c)
			got := workflowBottlenecks([]workflowBottleneckObservation{a, next}, &c, nil, start.Add(20*time.Second))
			maximumObserved := int64(10)
			if tc.name == "missing start" {
				// Both retained intervals are known; the time before revision 2 is not.
				maximumObserved = 20
			}
			if got.HistoryComplete || !slices.Contains(got.IncompleteReasons, tc.reason) || got.StateSeconds > maximumObserved {
				t.Fatalf("unknown range was invented: %+v", got)
			}
		})
	}
	b = workflowBottlenecks(nil, nil, []api.OperationWorkflowResolutionVerification{{Status: "awaiting_verification", Resolution: api.OperationWorkflowBlockerResolution{VerificationOwner: "operator"}}}, start)
	if b.HistoryComplete || b.VerificationUnknownStartCount != 1 || b.VerificationWaitSeconds != 0 || !slices.Contains(b.IncompleteReasons, "verification_start_missing") {
		t.Fatalf("unknown verification start must not invent a wait: %+v", b)
	}
}

func TestWorkflowDiagnosticsAttentionCursorIsolation(t *testing.T) {
	account, tenant, app := uuid.NewString(), uuid.NewString(), uuid.NewString()
	opts := api.OperationWorkflowAttentionOptions{AppID: app, Scope: "prod", Workflow: "recovery", Limit: 1}
	normal, cursor, err := prepareOperationAttention(account, tenant, opts, false)
	if err != nil {
		t.Fatal(err)
	}
	key := operationAttentionKey(tenant, api.OperationSubject{Type: "database", ID: "db-1"}, "recovery", "restore-1")
	rows := []operationAttentionRow{
		{Key: key, Entry: api.OperationWorkflowAttentionEntry{OperationID: "new", State: api.OperationWorkflowState{UpdatedAt: cursor.EvaluatedAt}}},
		{Key: strings.Repeat("a", 64), Entry: api.OperationWorkflowAttentionEntry{OperationID: "old", State: api.OperationWorkflowState{UpdatedAt: cursor.EvaluatedAt.Add(-time.Second)}}},
	}
	page := operationAttentionPage(rows, 1, cursor)
	if page.NextCursor == "" || len(page.Items) != 1 || page.Items[0].OperationID != "new" {
		t.Fatalf("page=%+v", page)
	}
	normal.Cursor = page.NextCursor
	normal.Limit = 2
	_, continuation, err := prepareOperationAttention(account, tenant, normal, false)
	if err != nil || !continuation.EvaluatedAt.Equal(page.EvaluatedAt) || continuation.Key != key {
		t.Fatalf("continuation=%+v %v", continuation, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*api.OperationWorkflowAttentionOptions)
	}{
		{"app", func(o *api.OperationWorkflowAttentionOptions) { o.AppID = uuid.NewString() }},
		{"scope", func(o *api.OperationWorkflowAttentionOptions) { o.Scope = "preview" }},
		{"workflow", func(o *api.OperationWorkflowAttentionOptions) { o.Workflow = "provision" }},
		{"operation", func(o *api.OperationWorkflowAttentionOptions) { o.TargetOperation = "restore" }},
		{"owner", func(o *api.OperationWorkflowAttentionOptions) { o.Owner = "operator" }},
		{"priority", func(o *api.OperationWorkflowAttentionOptions) { o.Priority = "high" }},
		{"sort", func(o *api.OperationWorkflowAttentionOptions) { o.Sort = "deadline" }},
		{"reason", func(o *api.OperationWorkflowAttentionOptions) { o.Reason = "blocked" }},
		{"blocker", func(o *api.OperationWorkflowAttentionOptions) { o.BlockerCode = "provider-wait" }},
		{"outcome", func(o *api.OperationWorkflowAttentionOptions) { o.RequiredOutcomeCode = "ready" }},
		{"dependency", func(o *api.OperationWorkflowAttentionOptions) { o.DependencyStatus = "waiting" }},
		{"tenant override", func(o *api.OperationWorkflowAttentionOptions) { o.TenantID = tenant }},
		{"invalid owner", func(o *api.OperationWorkflowAttentionOptions) { o.Owner = "operator\n" }},
		{"ambiguous owner", func(o *api.OperationWorkflowAttentionOptions) { o.Owner = "operator"; o.Unassigned = true }},
		{"invalid reason", func(o *api.OperationWorkflowAttentionOptions) { o.Reason = "everything" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := normal
			tc.mutate(&changed)
			if _, _, err := prepareOperationAttention(account, tenant, changed, false); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("cursor crossed query boundary: %v", err)
			}
		})
	}
	for _, identity := range []struct{ account, tenant string }{{uuid.NewString(), tenant}, {account, uuid.NewString()}} {
		if _, _, err := prepareOperationAttention(identity.account, identity.tenant, normal, false); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("cursor crossed account/tenant", err)
		}
	}
	if _, _, err := prepareOperationAttention(account, tenant, normal, false, "workflow-performance"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("cursor crossed projection domain", err)
	}
	encode := func(c operationAttentionCursor) string {
		raw, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	for _, mutate := range []func(*operationAttentionCursor){
		func(c *operationAttentionCursor) { c.Version = 2 }, func(c *operationAttentionCursor) { c.Key = "bad" },
		func(c *operationAttentionCursor) { c.UpdatedAt = time.Time{} }, func(c *operationAttentionCursor) { c.EvaluatedAt = cursor.EvaluatedAt.Add(time.Hour) },
		func(c *operationAttentionCursor) { c.DeadlineAt = "2026-01-01T00:00:00Z" },
	} {
		c := continuation
		mutate(&c)
		changed := normal
		changed.Cursor = encode(c)
		if _, _, err := prepareOperationAttention(account, tenant, changed, false); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("invalid cursor accepted", err)
		}
	}
	for _, raw := range []string{`{"unexpected":1}`, `{} {}`, `not json`} {
		changed := normal
		changed.Cursor = base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, _, err := prepareOperationAttention(account, tenant, changed, false); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("malformed cursor accepted", err)
		}
	}
	deadlineOpts := opts
	deadlineOpts.Sort = "deadline"
	_, deadlineCursor, err := prepareOperationAttention(account, tenant, deadlineOpts, false)
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Entry.State.DeadlineAt = "2026-01-02T00:00:00Z"
	rows[1].Entry.State.DeadlineAt = "2026-01-01T00:00:00Z"
	page = operationAttentionPage(rows, 1, deadlineCursor)
	if page.Items[0].OperationID != "old" {
		t.Fatal("deadline ordering must take precedence over update time")
	}
	deadlineOpts.Cursor = page.NextCursor
	if _, _, err := prepareOperationAttention(account, tenant, deadlineOpts, false); err != nil {
		t.Fatal(err)
	}
	deadlineCursor.UpdatedAt = cursor.EvaluatedAt
	deadlineCursor.Key = key
	deadlineCursor.DeadlineAt = "invalid"
	deadlineOpts.Cursor = encode(deadlineCursor)
	if _, _, err := prepareOperationAttention(account, tenant, deadlineOpts, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("invalid deadline accepted", err)
	}
}

func TestWorkflowDiagnosticsReadinessRequiresRecoveryEvidence(t *testing.T) {
	dependency := api.OperationWorkflowDependency{SubjectType: "database", SubjectID: "db-1", Workflow: "backup", InstanceID: "backup-1"}
	req := api.OperationWorkflowReadinessRequest{AppID: uuid.NewString(), Scope: "prod", Subject: api.OperationSubject{Type: "database", ID: "db-1"}, Workflow: "recovery", InstanceID: "restore-1", Operation: "restore", FromState: "restoring", ToState: "ready", StateRevision: 2, ContractVersion: 1}
	edge := api.OperationWorkflowInstanceTransition{Operation: req.Operation, From: req.FromState, To: req.ToState,
		RequiredMilestones: []string{"verified"}, RequiredDependencyWorkflows: &[]string{"backup"},
		RequiredEffects:    []api.OperationWorkflowEffectRequirement{{Milestone: "restored", Code: "restore", Version: "v1"}},
		RequiredInvariants: []api.OperationWorkflowInvariantRequirement{{Milestone: "checked", Code: "consistent", Version: "v1"}},
		RequiredPolicies:   []api.OperationWorkflowPolicyRequirement{{Milestone: "approved", RuleID: "recovery-policy", RuleVersion: "v1", Code: "allow"}}}
	instance := &api.OperationWorkflowInstanceSnapshot{ContractVersion: 1, State: &api.OperationWorkflowState{State: "restoring", Revision: 2, Stale: true, Overdue: true, DependsOn: []api.OperationWorkflowDependency{dependency}}, AllowedTransitions: []api.OperationWorkflowInstanceTransition{edge}, RelatedWorkflows: []api.OperationWorkflowRelatedInstance{{Dependency: dependency, Status: "waiting", State: &api.OperationWorkflowState{State: "private"}}}}
	out := evaluateOperationWorkflowReadiness(instance, req)
	for _, reason := range []string{"dependency_unmet", "milestone_required", "policy_evidence_required", "invariant_evidence_required", "effect_evidence_required"} {
		if !slices.Contains(out.Reasons, reason) {
			t.Fatalf("missing %s: %+v", reason, out)
		}
	}
	if out.Ready || len(out.UnmetDependencies) != 1 || out.UnmetDependencies[0].State != nil {
		t.Fatal("readiness must fail closed without leaking dependency detail", out)
	}
	req.Milestones = []string{"verified", "restored", "checked", "approved"}
	req.Effects = []api.OperationWorkflowPlannedEffect{{Milestone: "restored", Effect: api.OperationBusinessEffect{Workflow: req.Workflow, InstanceID: req.InstanceID, State: req.ToState, Operation: req.Operation, Code: "restore", Version: "v1", Status: "confirmed", Reference: "provider-restore", Description: "Restored database"}}}
	req.Invariants = []api.OperationWorkflowPlannedInvariant{{Milestone: "checked", Invariant: api.OperationBusinessInvariant{Workflow: req.Workflow, InstanceID: req.InstanceID, State: req.FromState, Operations: []string{req.Operation}, Code: "consistent", Version: "v1", Status: "passed", Description: "Recovery consistency verified"}}}
	req.Decisions = []api.OperationWorkflowPlannedDecision{{Milestone: "approved", Decision: api.OperationBusinessDecision{Workflow: req.Workflow, InstanceID: req.InstanceID, RuleID: "recovery-policy", RuleVersion: "v1", Code: "allow", Description: "Recovery approved"}}}
	instance.RelatedWorkflows[0].Status = "satisfied"
	if err := validateOperationWorkflowReadiness(req, false); err != nil {
		t.Fatal(err)
	}
	out = evaluateOperationWorkflowReadiness(instance, req)
	if !out.Ready || !reflect.DeepEqual(out.Advisories, []string{"state_stale", "deadline_overdue"}) {
		t.Fatalf("complete evidence should be ready with timing advisories: %+v", out)
	}
	for _, tc := range []struct {
		name, reason string
		mutate       func(*api.OperationWorkflowInstanceSnapshot, *api.OperationWorkflowReadinessRequest)
	}{
		{"stale revision", "revision_mismatch", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			r.StateRevision = 1
		}},
		{"stale contract", "contract_version_mismatch", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			r.ContractVersion = 2
		}},
		{"terminal", "terminal", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			i.State.Terminal = true
		}},
		{"different state", "from_state_mismatch", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			i.State.State = "pending"
		}},
		{"unknown state", "state_unknown", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			i.State = nil
		}},
		{"undeclared edge", "transition_undeclared", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			i.AllowedTransitions = nil
		}},
		{"missing prerequisite", "dependency_required", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			i.State.DependsOn = nil
		}},
		{"invariant blocker", "application_blocked", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			i.State.Blockers = []api.OperationWorkflowBlocker{{Operation: r.Operation, Code: "invariant-consistent"}, {Operation: "other", Code: "unrelated"}}
		}},
		{"missing fact", "milestone_required", func(i *api.OperationWorkflowInstanceSnapshot, r *api.OperationWorkflowReadinessRequest) {
			r.Milestones = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := *instance
			s := *instance.State
			i.State = &s
			r := req
			tc.mutate(&i, &r)
			got := evaluateOperationWorkflowReadiness(&i, r)
			if got.Ready || !slices.Contains(got.Reasons, tc.reason) {
				t.Fatalf("recovery fence missing: %+v", got)
			}
			if tc.name == "invariant blocker" && (len(got.Blockers) != 1 || len(got.InvariantBlockers) != 1) {
				t.Fatal("selected operation blockers were not isolated", got)
			}
		})
	}
	if got := evaluateOperationWorkflowReadiness(nil, req); got.Ready || !slices.Contains(got.Reasons, "state_unknown") {
		t.Fatal("unknown instance accepted", got)
	}
	for _, mutate := range []func(*api.OperationWorkflowReadinessRequest){
		func(r *api.OperationWorkflowReadinessRequest) { r.Milestones = []string{"verified", "verified"} },
		func(r *api.OperationWorkflowReadinessRequest) { r.Effects = append(r.Effects, r.Effects[0]) },
		func(r *api.OperationWorkflowReadinessRequest) { r.Invariants = append(r.Invariants, r.Invariants[0]) },
		func(r *api.OperationWorkflowReadinessRequest) { r.Decisions = append(r.Decisions, r.Decisions[0]) },
		func(r *api.OperationWorkflowReadinessRequest) {
			r.Decisions = []api.OperationWorkflowPlannedDecision{{Milestone: "approved", Decision: api.OperationBusinessDecision{}}}
		},
		func(r *api.OperationWorkflowReadinessRequest) { r.TenantID = uuid.NewString() },
	} {
		r := req
		mutate(&r)
		if !errors.Is(validateOperationWorkflowReadiness(r, false), ErrInvalidArgument) {
			t.Fatal("ambiguous or unscoped evidence accepted")
		}
	}
	page := api.OperationMilestonesResponse{WorkflowInstance: &api.OperationWorkflowInstanceSnapshot{State: &api.OperationWorkflowState{State: req.FromState}, ContractVersion: 1}}
	for n := 0; n < 105; n++ {
		page.WorkflowInstance.AllowedTransitions = append(page.WorkflowInstance.AllowedTransitions, api.OperationWorkflowInstanceTransition{Operation: fmt.Sprintf("restore-%d", n), From: req.FromState, To: req.ToState})
	}
	page.WorkflowInstance.AllowedTransitions = append(page.WorkflowInstance.AllowedTransitions, api.OperationWorkflowInstanceTransition{From: "other", To: "ready", Operation: "other"})
	projectOperationWorkflowReadiness(&page)
	if overview := page.WorkflowInstance.Readiness; overview.TransitionCount != 105 || len(overview.Items) != 100 || !overview.HasMore {
		t.Fatalf("unbounded or incorrect readiness overview: %+v", overview)
	}
	page.WorkflowInstance.State.Terminal = true
	projectOperationWorkflowReadiness(&page)
	if page.WorkflowInstance.Readiness.TransitionCount != 0 {
		t.Fatal("terminal instance exposed runnable edges")
	}
}

func TestWorkflowDiagnosticsPerformanceExcludesUnknownHistory(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := start.Add(100 * time.Second)
	var instances []workflowPerformanceInstance
	for n, seconds := range []int64{10, 20, 30, 40, 100} {
		first := diagnosticsHistory(1, "restoring", "", at.Add(-time.Duration(seconds)*time.Second))
		first.SLABudgetSeconds = 30
		last := diagnosticsHistory(2, "ready", "restoring", at)
		last.Terminal = true
		instances = append(instances, workflowPerformanceInstance{SLAConfigured: true, CohortTotal: 10, Current: diagnosticsCurrent(last), Observations: []workflowBottleneckObservation{first, last}})
		instances[n].Observations[0].History.Blockers = []api.OperationWorkflowBlocker{{Operation: "restore", Code: "provider-wait", Owner: "provider"}}
		instances[n].Verifications = []api.OperationWorkflowResolutionVerification{{ResolutionOperationID: first.History.OperationID, ResolutionReportID: first.History.ID, Status: "awaiting_verification", Resolution: api.OperationWorkflowBlockerResolution{VerificationOwner: "operator"}}}
	}
	unknown := workflowPerformanceInstance{SLAConfigured: true, CohortTotal: 10, Current: api.OperationWorkflowState{State: "restoring", Revision: 4}}
	instances = append(instances, unknown)
	summary := workflowPerformanceSummary(instances, api.OperationWorkflowPerformanceOptions{Workflow: "recovery"}, at)
	c := summary.Completed
	if c.SampledWorkflowCount != 5 || c.CompleteHistoryWorkflowCount != 5 || c.StateTime.WorkflowCount != 5 || c.StateTime.TotalSeconds != 200 || c.StateTime.P50Seconds != 30 || c.StateTime.P95Seconds != 100 || c.BlockedTime.TotalSeconds != 200 || c.VerificationWait.TotalSeconds != 200 || !c.CohortTruncated {
		t.Fatalf("duration cohort included missing histories or wrong percentiles: %+v", c)
	}
	if c.SLAConfiguredWorkflowCount != 5 || c.SLAEvaluatedWorkflowCount != 5 || c.SLABreachedWorkflowCount != 3 || c.States[0].SLABreachedVisitCount != 3 || c.VerificationOwners[0].PendingResolutionCount != 5 {
		t.Fatalf("lost SLA or verification obligations: %+v", c)
	}
	u := summary.Ongoing
	if u.SampledWorkflowCount != 1 || u.ExcludedIncompleteWorkflowCount != 1 || u.CompleteHistoryWorkflowCount != 0 || u.StateTime.WorkflowCount != 0 || u.SLAUnknownWorkflowCount != 1 || len(u.Exclusions) != 2 {
		t.Fatalf("unknown workflow was reported as a zero-duration success: %+v", u)
	}
	for _, tc := range []struct {
		elapsed             int64
		status              string
		remaining, breached int64
	}{{0, "within_budget", 100, 0}, {79, "within_budget", 21, 0}, {80, "at_risk", 20, 0}, {100, "breached", 0, 0}, {110, "breached", 0, 10}} {
		first := diagnosticsHistory(1, "restoring", "", start)
		first.SLABudgetSeconds = 100
		first.SLAWarningPercent = 80
		current := diagnosticsCurrent(first)
		sla := workflowStateSLA(current, []workflowBottleneckObservation{first}, 100, 80, start.Add(time.Duration(tc.elapsed)*time.Second))
		if sla == nil || !sla.HistoryComplete || sla.Status != tc.status || *sla.ElapsedSeconds != tc.elapsed || *sla.RemainingSeconds != tc.remaining || *sla.BreachedSeconds != tc.breached || !sla.DueAt.Equal(start.Add(100*time.Second)) {
			t.Fatalf("SLA boundary %+v: %+v", tc, sla)
		}
	}
	first := diagnosticsHistory(1, "restoring", "", start)
	first.SLABudgetSeconds = 100
	first.SLAWarningPercent = 80
	current := diagnosticsCurrent(first)
	for _, rows := range [][]workflowBottleneckObservation{nil, {first, first}} {
		if sla := workflowStateSLA(current, rows, 100, 80, at); sla.Status != "unknown" || sla.HistoryComplete || sla.ElapsedSeconds != nil {
			t.Fatalf("incomplete SLA invented elapsed time: %+v", sla)
		}
	}
	first.SLABudgetSeconds = 99
	if sla := workflowStateSLA(current, []workflowBottleneckObservation{first}, 100, 80, at); sla.Status != "unknown" {
		t.Fatal("changed budget reused old visit evidence", sla)
	}
}
