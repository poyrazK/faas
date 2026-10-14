package issuetrackers

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s LinearService) EnableAutoSync(ctx context.Context, store state.IssueTrackerAutoSyncStore, issue string, in api.UpdateIssueTrackerAutoSyncRequest) (api.IssueTrackerAutoSync, error) {
	if in.ExpectedStatus != nil || in.ExpectedLinearStatus == nil || in.ExpectedRevision < 0 || (in.ExpectedIssueState != "open" && in.ExpectedIssueState != "resolved") {
		return api.IssueTrackerAutoSync{}, api.ErrValidation("Linear delegation requires one native Linear snapshot, source lifecycle and policy revision")
	}
	p, err := s.prepare(ctx)
	if err != nil {
		return api.IssueTrackerAutoSync{}, err
	}
	observed, err := s.VerifyWorkflowMapping(ctx, LinearWorkflowMapping{OpenStateID: in.LinearOpenStateID, ResolvedStateID: in.LinearResolvedStateID})
	if err != nil {
		return api.IssueTrackerAutoSync{}, err
	}
	if err := s.fresh(ctx, p); err != nil {
		return api.IssueTrackerAutoSync{}, err
	}
	if observed != *in.ExpectedLinearStatus || p.Issue.State != in.ExpectedIssueState {
		return api.IssueTrackerAutoSync{}, Changed()
	}
	if observed.Status.State != p.Issue.State {
		return api.IssueTrackerAutoSync{}, api.NewProblem(409, "issue_tracker_auto_sync_requires_alignment", "Align lifecycle before enabling", "explicitly synchronize Gregale and Linear before enabling automatic push")
	}
	open, _ := uuid.Parse(in.LinearOpenStateID)
	resolved, _ := uuid.Parse(in.LinearResolvedStateID)
	row, err := store.ConfigureIssueTrackerAutoSync(ctx, p.Issue.AppID, p.Connection.AccountID, issue, state.IssueTrackerAutoSyncRecord{BindingFingerprint: autoSyncFingerprint(p), Baseline: observed.Status, Policy: api.IssueTrackerAutoSync{LinkID: p.Link.ID, Enabled: true, SourceIssueID: p.Issue.ID, SourceState: p.Issue.State, LinearOpenStateID: open.String(), LinearResolvedStateID: resolved.String()}}, in.ExpectedRevision)
	return row.Policy, err
}
func linearDestination(p api.IssueTrackerAutoSync, state string) string {
	if state == "resolved" {
		return p.LinearResolvedStateID
	}
	return p.LinearOpenStateID
}

// LinearAutoSyncStep uses the policy's immutable workflow mapping to recognize
// uncertain writes. Unconfirmed pending writes are observed, never redispatched.
func LinearAutoSyncStep(ctx context.Context, s LinearService, store state.IssueTrackerAutoSyncStore, r state.IssueTrackerAutoSyncRecord) state.IssueTrackerAutoSyncRecord {
	s.Host = autoSyncHost{Host: s.Host, store: store, record: r}
	var snapshot api.NativeLinearIssueTrackerStatus
	return autoSyncRun(ctx, s.prepare, func(ctx context.Context) (api.IssueTrackerProviderStatus, error) {
		v, e := s.Read(ctx)
		snapshot = v
		return v.Status, e
	}, func(ctx context.Context, wanted string, expected api.IssueTrackerProviderStatus) (api.IssueTrackerProviderStatus, error) {
		v, e := s.Push(ctx, api.UpdateNativeLinearIssueTrackerStatusRequest{DesiredState: wanted, Expected: expected, ExpectedConnectionRevision: snapshot.ConnectionRevision, ExpectedGroupFingerprint: snapshot.GroupFingerprint, LinearStateID: linearDestination(r.Policy, wanted)})
		return v.Status, e
	}, store, r)
}
