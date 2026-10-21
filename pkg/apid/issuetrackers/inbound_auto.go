package issuetrackers

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func autoSyncSourceBaseline(ctx context.Context, store state.IssueTrackerAutoSyncStore, p Prepared, direction string) (int64, error) {
	if direction == "outbound" {
		return 0, nil
	}
	inbound, ok := store.(state.IssueTrackerInboundAutoStore)
	if !ok {
		return 0, api.ErrCapacity("inbound sync persistence unavailable")
	}
	return inbound.GetIssueTrackerSourceRevision(ctx, p.Issue.AppID, p.Connection.AccountID, p.Issue.ID)
}

type sourceIntentHost struct {
	Host
	store    state.IssueTrackerInboundAutoStore
	record   state.IssueTrackerAutoSyncRecord
	expected int64
}

func (h sourceIntentHost) Prepare(ctx context.Context) (Prepared, error) {
	before, err := h.store.GetIssueTrackerSourceRevision(ctx, h.record.AppID, h.record.AccountID, h.record.Policy.SourceIssueID)
	if err != nil {
		return Prepared{}, err
	}
	if before != h.expected {
		return Prepared{}, state.IssueTrackerInboundChanged()
	}
	p, err := h.Host.Prepare(ctx)
	if err != nil {
		return p, err
	}
	after, err := h.store.GetIssueTrackerSourceRevision(ctx, h.record.AppID, h.record.AccountID, h.record.Policy.SourceIssueID)
	if err != nil {
		return p, err
	}
	if after != before {
		return p, state.IssueTrackerInboundChanged()
	}
	return p, nil
}

func directionalHost(ctx context.Context, host Host, store state.IssueTrackerAutoSyncStore, r state.IssueTrackerAutoSyncRecord) (Host, state.IssueTrackerInboundAutoStore, int64, error) {
	inbound, ok := store.(state.IssueTrackerInboundAutoStore)
	if !ok {
		return nil, nil, 0, api.ErrCapacity("inbound sync persistence unavailable")
	}
	current, err := inbound.GetIssueTrackerSourceRevision(ctx, r.AppID, r.AccountID, r.Policy.SourceIssueID)
	if err != nil {
		return nil, inbound, 0, err
	}
	return sourceIntentHost{Host: autoSyncHost{Host: host, store: store, record: r}, store: inbound, record: r, expected: current}, inbound, current, nil
}
func githubDirectionalSyncStep(ctx context.Context, s Service, store state.IssueTrackerAutoSyncStore, r state.IssueTrackerAutoSyncRecord) state.IssueTrackerAutoSyncRecord {
	host, inbound, current, err := directionalHost(ctx, s.Host, store, r)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	s.Host = host
	return directionalSyncRun(ctx, s.prepare, func(ctx context.Context) (api.IssueTrackerProviderStatus, error) {
		v, e := s.Read(ctx)
		return v.Status, e
	}, func(ctx context.Context, wanted string, expected api.IssueTrackerProviderStatus) (api.IssueTrackerProviderStatus, error) {
		v, e := s.Push(ctx, api.UpdateNativeIssueTrackerStatusRequest{DesiredState: wanted, Expected: expected})
		return v.Status, e
	}, store, inbound, r, current)
}
func linearDirectionalSyncStep(ctx context.Context, s LinearService, store state.IssueTrackerAutoSyncStore, r state.IssueTrackerAutoSyncRecord) state.IssueTrackerAutoSyncRecord {
	host, inbound, current, err := directionalHost(ctx, s.Host, store, r)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	s.Host = host
	var snapshot api.NativeLinearIssueTrackerStatus
	return directionalSyncRun(ctx, s.prepare, func(ctx context.Context) (api.IssueTrackerProviderStatus, error) {
		v, e := s.Read(ctx)
		snapshot = v
		return v.Status, e
	}, func(ctx context.Context, wanted string, expected api.IssueTrackerProviderStatus) (api.IssueTrackerProviderStatus, error) {
		v, e := s.Push(ctx, api.UpdateNativeLinearIssueTrackerStatusRequest{DesiredState: wanted, Expected: expected, ExpectedConnectionRevision: snapshot.ConnectionRevision, ExpectedGroupFingerprint: snapshot.GroupFingerprint, LinearStateID: linearDestination(r.Policy, wanted)})
		return v.Status, e
	}, store, inbound, r, current)
}

func directionalPause(r state.IssueTrackerAutoSyncRecord, outcome, reason string) state.IssueTrackerAutoSyncRecord {
	r.Policy.Outcome, r.Policy.Reason = outcome, reason
	return r
}

func directionalSyncRun(ctx context.Context, prepare func(context.Context) (Prepared, error), read func(context.Context) (api.IssueTrackerProviderStatus, error), push func(context.Context, string, api.IssueTrackerProviderStatus) (api.IssueTrackerProviderStatus, error), store state.IssueTrackerAutoSyncStore, inbound state.IssueTrackerInboundAutoStore, r state.IssueTrackerAutoSyncRecord, current int64) state.IssueTrackerAutoSyncRecord {
	p, err := prepare(ctx)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	if !lifecycleState(p.Issue.State) {
		return directionalPause(r, "blocked", "source_state_unsupported")
	}
	observed, err := read(ctx)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	r.Policy.ObservedState = observed.State
	if r.Policy.PendingState != "" {
		return recoverDirectionalPending(r, p, observed, current)
	}
	localChanged := current != r.SourceRevision
	remoteChanged := observed != r.Baseline
	if r.SourceRevision <= 0 {
		return directionalPause(r, "blocked", "source_revision_required")
	}
	if localChanged {
		if remoteChanged || r.Policy.Direction == "inbound" || p.Issue.State == r.Policy.SourceState {
			return directionalPause(r, "conflict", "source_intent_changed")
		}
		// Both directions: only an isolated local lifecycle change may push. Retain
		// its source revision before dispatch so uncertain writes cannot grant a
		// later inbound update authority over intervening manual edits.
		r.PendingSourceRevision = current
		r, err = store.MarkIssueTrackerAutoSyncPending(ctx, r, p.Issue.State)
		if err != nil {
			return autoSyncFailure(r, err)
		}
		out, e := push(ctx, p.Issue.State, observed)
		if e != nil {
			return autoSyncFailure(r, e)
		}
		r.Baseline = out
		r.SourceRevision = current
		r.PendingSourceRevision = 0
		r.Policy.SourceState = p.Issue.State
		r.Policy.ObservedState = out.State
		r.Policy.PendingState = ""
		return directionalPause(r, "applied", "")
	}
	if p.Issue.State != r.Policy.SourceState || p.Issue.State != r.Baseline.State {
		return directionalPause(r, "conflict", "source_intent_changed")
	}
	if !remoteChanged {
		return directionalPause(r, "matched", "")
	}
	if !sameProviderIdentity(r.Baseline, observed) {
		return directionalPause(r, "conflict", "provider_changed")
	}
	if observed.Provider == "linear" && observed.StateID != linearDestination(r.Policy, observed.State) {
		return directionalPause(r, "blocked", "provider_workflow_unmapped")
	}
	if observed.State == r.Baseline.State {
		return directionalPause(r, "conflict", "provider_changed")
	}
	if observed.State == "resolved" {
		return directionalPause(r, "blocked", "fixing_deployment_required")
	}
	out, err := inbound.ApplyIssueTrackerAutoReopen(ctx, r, observed)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	return out
}

func sameProviderIdentity(a, b api.IssueTrackerProviderStatus) bool {
	return a.ID != "" && a.ID == b.ID && a.Provider == b.Provider && a.URL == b.URL && a.TeamID == b.TeamID
}
func recoverDirectionalPending(r state.IssueTrackerAutoSyncRecord, p Prepared, observed api.IssueTrackerProviderStatus, current int64) state.IssueTrackerAutoSyncRecord {
	expected := r.Baseline
	expected.State = r.Policy.PendingState
	if expected.Provider == "linear" {
		expected.StateID = linearDestination(r.Policy, expected.State)
	}
	if observed == expected {
		dispatched := r.PendingSourceRevision
		r.Baseline = observed
		r.Policy.SourceState = expected.State
		r.SourceRevision = dispatched
		r.Policy.PendingState = ""
		r.PendingSourceRevision = 0
		if dispatched <= 0 || current != dispatched || p.Issue.State != expected.State {
			return directionalPause(r, "conflict", "source_changed_after_dispatch")
		}
		return directionalPause(r, "applied", "")
	}
	if observed != r.Baseline {
		return directionalPause(r, "conflict", "provider_changed")
	}
	return directionalPause(r, "error", "pending_outcome_unconfirmed")
}
