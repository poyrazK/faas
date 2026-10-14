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
	data, _ := json.Marshal(struct {
		Connection       Connection
		GroupFingerprint string
	}{p.Connection, p.GroupFingerprint})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (s Service) EnableAutoSync(ctx context.Context, store state.IssueTrackerAutoSyncStore, issue string, in api.UpdateIssueTrackerAutoSyncRequest) (api.IssueTrackerAutoSync, error) {
	if in.ExpectedStatus == nil || (in.ExpectedIssueState != "open" && in.ExpectedIssueState != "resolved") || in.ExpectedRevision < 0 {
		return api.IssueTrackerAutoSync{}, api.ErrValidation("enabling requires a native snapshot, open/resolved source state and nonnegative revision")
	}
	p, err := s.prepare(ctx)
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
		return api.IssueTrackerAutoSync{}, api.NewProblem(409, "issue_tracker_auto_sync_requires_alignment", "Align lifecycle before enabling", "explicitly synchronize Gregale and GitHub before enabling automatic push")
	}
	row, err := store.ConfigureIssueTrackerAutoSync(ctx, p.Issue.AppID, p.Connection.AccountID, issue, state.IssueTrackerAutoSyncRecord{BindingFingerprint: autoSyncFingerprint(p), Baseline: observed.Status, Policy: api.IssueTrackerAutoSync{LinkID: p.Link.ID, Enabled: true, SourceIssueID: p.Issue.ID, SourceState: p.Issue.State}}, in.ExpectedRevision)
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
	s.Host = autoSyncHost{Host: s.Host, store: store, record: r}
	p, err := s.prepare(ctx)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	if p.Issue.State != "open" && p.Issue.State != "resolved" {
		r.Policy.Outcome = "blocked"
		r.Policy.Reason = "source_state_unsupported"
		return r
	}
	observed, err := s.Read(ctx)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	pending := r.Baseline
	pending.State = r.Policy.PendingState
	if r.Policy.PendingState != "" && observed.Status == pending {
		r.Baseline = observed.Status
		r.Policy.SourceState = pending.State
		r.Policy.PendingState = ""
		r.Policy.Outcome = "applied"
		r.Policy.Reason = ""
		return r
	}
	if observed.Status != r.Baseline {
		r.Policy.Outcome = "conflict"
		r.Policy.Reason = "provider_changed"
		return r
	}
	if r.Policy.PendingState != "" && r.Policy.PendingState != p.Issue.State {
		r.Policy.Outcome = "conflict"
		r.Policy.Reason = "source_changed_after_dispatch"
		return r
	}
	if p.Issue.State == observed.Status.State {
		r.Policy.SourceState = p.Issue.State
		r.Policy.Outcome = "matched"
		r.Policy.Reason = ""
		return r
	}
	r, err = store.MarkIssueTrackerAutoSyncPending(ctx, r, p.Issue.State)
	if err != nil {
		return autoSyncFailure(r, err)
	}
	out, err := s.Push(ctx, api.UpdateNativeIssueTrackerStatusRequest{DesiredState: p.Issue.State, Expected: observed.Status})
	if err != nil {
		return autoSyncFailure(r, err)
	}
	r.Baseline = out.Status
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
