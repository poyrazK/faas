package issuetrackers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func autoSyncFingerprint(p Prepared) string {
	if p.Connection.Linear != nil {
		data, _ := json.Marshal(struct {
			Connection       Connection
			GroupFingerprint string
			Revision         int64
		}{p.Connection, p.GroupFingerprint, p.Connection.Linear.Revision})
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	data, _ := json.Marshal(struct {
		Connection       Connection
		GroupFingerprint string
	}{p.Connection, p.GroupFingerprint})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (s Service) EnableAutoSync(ctx context.Context, store state.IssueTrackerAutoSyncStore, issue string, in api.UpdateIssueTrackerAutoSyncRequest) (api.IssueTrackerAutoSync, error) {
	if in.ExpectedLinearStatus != nil || in.LinearOpenStateID != "" || in.LinearResolvedStateID != "" {
		return api.IssueTrackerAutoSync{}, api.ErrValidation("Linear mappings require a Linear native snapshot")
	}
	if in.ExpectedStatus == nil || (in.ExpectedIssueState != "open" && in.ExpectedIssueState != "resolved") || in.ExpectedRevision < 0 {
		return api.IssueTrackerAutoSync{}, api.ErrValidation("enabling requires a native snapshot, open/resolved source state and nonnegative revision")
	}
	direction, err := api.NormalizeIssueTrackerSyncDirection(in.Direction)
	if err != nil {
		return api.IssueTrackerAutoSync{}, err
	}
	p, err := s.prepare(ctx)
	if err != nil {
		return api.IssueTrackerAutoSync{}, err
	}
	source, err := autoSyncSourceBaseline(ctx, store, p, direction)
	if err != nil {
		return api.IssueTrackerAutoSync{}, err
	}
	observed, err := s.Read(ctx)
	if err != nil {
		return api.IssueTrackerAutoSync{}, err
	}
	fresh, err := s.prepare(ctx)
	if err != nil {
		return api.IssueTrackerAutoSync{}, err
	}
	if !same(p, fresh) || observed != *in.ExpectedStatus || p.Issue.State != in.ExpectedIssueState {
		return api.IssueTrackerAutoSync{}, Changed()
	}
	if observed.Status.State != p.Issue.State {
		return api.IssueTrackerAutoSync{}, api.NewProblem(409, "issue_tracker_auto_sync_requires_alignment", "Align lifecycle before enabling", "explicitly synchronize Gregale and GitHub before enabling automatic sync")
	}
	row, err := store.ConfigureIssueTrackerAutoSync(ctx, p.Issue.AppID, p.Connection.AccountID, issue, state.IssueTrackerAutoSyncRecord{SourceRevision: source, BindingFingerprint: autoSyncFingerprint(p), Baseline: observed.Status, Policy: api.IssueTrackerAutoSync{Direction: direction, LinkID: p.Link.ID, Enabled: true, SourceIssueID: p.Issue.ID, SourceState: p.Issue.State}}, in.ExpectedRevision)
	return row.Policy, err
}

type autoSyncHost struct {
	Host
	store  state.IssueTrackerAutoSyncStore
	record state.IssueTrackerAutoSyncRecord
}

func (h autoSyncHost) Prepare(ctx context.Context) (Prepared, error) {
	if err := h.store.CheckIssueTrackerAutoSyncClaim(ctx, h.record); err != nil {
		return Prepared{}, err
	}
	p, err := h.Host.Prepare(ctx)
	if err != nil {
		return p, err
	}
	if p.Issue.ID != h.record.Policy.SourceIssueID || autoSyncFingerprint(p) != h.record.BindingFingerprint {
		return p, Changed()
	}
	return p, nil
}

// AutoSyncStep persists the pending intent and uses the same scoped native read/push safeguards.
// The caller finishes the claimed record even after a canceled provider context.
func AutoSyncStep(ctx context.Context, s Service, store state.IssueTrackerAutoSyncStore, r state.IssueTrackerAutoSyncRecord) state.IssueTrackerAutoSyncRecord {
	if r.Policy.Direction == "inbound" || r.Policy.Direction == "both" {
		return githubDirectionalSyncStep(ctx, s, store, r)
	}
	s.Host = autoSyncHost{Host: s.Host, store: store, record: r}
	return autoSyncRun(ctx, s.prepare, func(ctx context.Context) (api.IssueTrackerProviderStatus, error) {
		v, e := s.Read(ctx)
		return v.Status, e
	}, func(ctx context.Context, wanted string, expected api.IssueTrackerProviderStatus) (api.IssueTrackerProviderStatus, error) {
		v, e := s.Push(ctx, api.UpdateNativeIssueTrackerStatusRequest{DesiredState: wanted, Expected: expected})
		return v.Status, e
	}, store, r)
}

func autoSyncRun(ctx context.Context, prepare func(context.Context) (Prepared, error), read func(context.Context) (api.IssueTrackerProviderStatus, error), push func(context.Context, string, api.IssueTrackerProviderStatus) (api.IssueTrackerProviderStatus, error), store state.IssueTrackerAutoSyncStore, r state.IssueTrackerAutoSyncRecord) state.IssueTrackerAutoSyncRecord {
	p, err := prepare(ctx)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	if p.Issue.State != "open" && p.Issue.State != "resolved" {
		r.Policy.Outcome = "blocked"
		r.Policy.Reason = "source_state_unsupported"
		return r
	}
	observed, err := read(ctx)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	r.Policy.ObservedState = observed.State
	pending := r.Baseline
	pending.State = r.Policy.PendingState
	if pending.Provider == "linear" && pending.State != "" {
		pending.StateID = linearDestination(r.Policy, pending.State)
	}
	if r.Policy.PendingState != "" && observed == pending {
		r.Baseline = observed
		r.Policy.SourceState = pending.State
		r.Policy.PendingState = ""
		r.Policy.Outcome = "applied"
		r.Policy.Reason = ""
		return r
	}
	if observed != r.Baseline {
		r.Policy.Outcome = "conflict"
		r.Policy.Reason = "provider_changed"
		return r
	}
	if r.Policy.PendingState != "" && r.Policy.PendingState != p.Issue.State {
		r.Policy.Outcome = "conflict"
		r.Policy.Reason = "source_changed_after_dispatch"
		return r
	}
	if r.Policy.PendingState != "" {
		r.Policy.Outcome = "error"
		r.Policy.Reason = "pending_outcome_unconfirmed"
		return r
	}
	if p.Issue.State == observed.State {
		r.Policy.SourceState = p.Issue.State
		r.Policy.Outcome = "matched"
		r.Policy.Reason = ""
		return r
	}
	r, err = store.MarkIssueTrackerAutoSyncPending(ctx, r, p.Issue.State)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	out, err := push(ctx, p.Issue.State, observed)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	r.Policy.ObservedState = out.State
	r.Baseline = out
	r.Policy.SourceState = p.Issue.State
	r.Policy.PendingState = ""
	r.Policy.Outcome = "applied"
	r.Policy.Reason = ""
	return r
}
func autoSyncFailure(r state.IssueTrackerAutoSyncRecord, err error) state.IssueTrackerAutoSyncRecord {
	r.Policy.Outcome = "error"
	r.Policy.Reason = "provider_unavailable"
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrIssueTrackerAutoSyncClaimLost) {
		r.Policy.Outcome = "blocked"
		r.Policy.Reason = "reference_or_policy_changed"
		return r
	}
	var problem *api.Problem
	if errors.As(err, &problem) {
		switch problem.Code {
		case "issue_tracker_auto_sync_ineligible":
			r.Policy.Outcome = "blocked"
			r.Policy.Reason = "owner_or_entitlement_unavailable"
		case "issue_tracker_inbound_changed":
			r.Policy.Outcome = "conflict"
			r.Policy.Reason = "source_intent_changed"
		case "issue_tracker_connection_required":
			r.Policy.Outcome = "blocked"
			r.Policy.Reason = "connection_required"
		case "issue_tracker_connection_changed", "issue_tracker_status_changed":
			r.Policy.Outcome = "conflict"
			r.Policy.Reason = "source_connection_or_provider_changed"
		case "issue_tracker_status_unsupported":
			r.Policy.Outcome = "blocked"
			r.Policy.Reason = "provider_state_unsupported"
		}
	}
	return r
}

// InspectionFailureReason returns a stable code, never a provider error body.
func InspectionFailureReason(err error) string {
	return autoSyncFailure(state.IssueTrackerAutoSyncRecord{}, err).Policy.Reason
}
