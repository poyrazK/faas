package issuetrackers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

type autoStoreFake struct {
	r       state.IssueTrackerAutoSyncRecord
	pending int
}

func (s *autoStoreFake) GetIssueTrackerAutoSync(context.Context, string, string, string, string) (state.IssueTrackerAutoSyncRecord, error) {
	return s.r, nil
}
func (s *autoStoreFake) ConfigureIssueTrackerAutoSync(_ context.Context, _, _, _ string, r state.IssueTrackerAutoSyncRecord, revision int64) (state.IssueTrackerAutoSyncRecord, error) {
	if revision != s.r.Policy.Revision {
		return r, Changed()
	}
	r.Policy.Revision = revision + 1
	s.r = r
	return r, nil
}
func (s *autoStoreFake) ClaimIssueTrackerAutoSync(context.Context) (state.IssueTrackerAutoSyncRecord, error) {
	return s.r, nil
}
func (s *autoStoreFake) CheckIssueTrackerAutoSyncClaim(_ context.Context, r state.IssueTrackerAutoSyncRecord) error {
	if !s.r.Policy.Enabled || r.ClaimID != s.r.ClaimID || r.Policy.Revision != s.r.Policy.Revision {
		return state.ErrIssueTrackerAutoSyncClaimLost
	}
	return nil
}
func (s *autoStoreFake) MarkIssueTrackerAutoSyncPending(ctx context.Context, r state.IssueTrackerAutoSyncRecord, wanted string) (state.IssueTrackerAutoSyncRecord, error) {
	if err := s.CheckIssueTrackerAutoSyncClaim(ctx, r); err != nil {
		return r, err
	}
	s.pending++
	r.Policy.PendingState = wanted
	r.Policy.Outcome = "pending"
	s.r = r
	return r, nil
}
func (s *autoStoreFake) FinishIssueTrackerAutoSync(_ context.Context, r state.IssueTrackerAutoSyncRecord) (state.IssueTrackerAutoSyncRecord, error) {
	s.r = r
	return r, nil
}

type autoProviderFake struct {
	status        issuetracker.Status
	reads, writes int
	readErr       error
	uncertain     bool
	beforeSet     func()
}

func (p *autoProviderFake) ReadStatus(context.Context, string, string) (issuetracker.Status, error) {
	p.reads++
	return p.status, p.readErr
}
func (p *autoProviderFake) SetStatus(_ context.Context, expected issuetracker.Status, _, wanted, _ string) (issuetracker.Status, error) {
	if p.beforeSet != nil {
		p.beforeSet()
	}
	if p.status != expected {
		return p.status, issuetracker.ErrStatusChanged
	}
	p.writes++
	p.status.State = wanted
	if p.uncertain {
		return issuetracker.Status{}, errors.New("secret provider diagnostics")
	}
	return p.status, nil
}
func autoFixture() (Service, *creationHostFake, *autoStoreFake, *autoProviderFake) {
	_, host, _, _, _ := creationFixture()
	host.p.Link = api.IssueTrackerLink{ID: uuid.NewString(), Provider: "github", URL: "https://github.com/owner/repo/issues/42"}
	status := api.IssueTrackerProviderStatus{Provider: "github", URL: host.p.Link.URL, ID: "42", State: "open"}
	store := &autoStoreFake{r: state.IssueTrackerAutoSyncRecord{AppID: host.p.Issue.AppID, AccountID: host.p.Connection.AccountID, BindingFingerprint: autoSyncFingerprint(host.p), ClaimID: uuid.NewString(), ClaimUntil: time.Now().Add(time.Minute), Baseline: status, Policy: api.IssueTrackerAutoSync{LinkID: host.p.Link.ID, Enabled: true, Revision: 1, SourceIssueID: host.p.Issue.ID, SourceState: "open", Outcome: "matched"}}}
	provider := &autoProviderFake{status: toStatus(status)}
	return Service{Host: host, Provider: provider}, host, store, provider
}
func TestAutoSyncPushesOnlyLocalChangesAndRecoversUncertainWrite(t *testing.T) {
	service, host, store, provider := autoFixture()
	out := AutoSyncStep(t.Context(), service, store, store.r)
	if out.Policy.Outcome != "matched" || provider.writes != 0 || store.pending != 0 {
		t.Fatal(out.Policy)
	}
	host.p.Issue.State = "resolved"
	provider.uncertain = true
	out = AutoSyncStep(t.Context(), service, store, store.r)
	if out.Policy.Outcome != "error" || out.Policy.PendingState != "resolved" || provider.writes != 1 || store.pending != 1 {
		t.Fatal(out.Policy)
	}
	store.r = out
	out = AutoSyncStep(t.Context(), service, store, store.r)
	if out.Policy.Outcome != "applied" || out.Policy.PendingState != "" || out.Baseline.State != "resolved" || provider.writes != 1 {
		t.Fatal("uncertain write repeated or not recovered", out.Policy)
	}
	store.r = out
	provider.uncertain = false
	host.p.Issue.State = "open"
	out = AutoSyncStep(t.Context(), service, store, store.r)
	if out.Policy.Outcome != "applied" || out.Baseline.State != "open" || provider.writes != 2 {
		t.Fatal("regression not pushed", out.Policy)
	}
}
func TestAutoSyncPausesConflictsAndUnsupportedStates(t *testing.T) {
	cases := []struct {
		name, want string
		change     func(*creationHostFake, *autoStoreFake, *autoProviderFake)
	}{
		{"remote edit", "conflict", func(h *creationHostFake, s *autoStoreFake, p *autoProviderFake) { p.status.State = "resolved" }},
		{"binding changed", "conflict", func(h *creationHostFake, s *autoStoreFake, p *autoProviderFake) { h.p.Connection.RepositoryID++ }},
		{"group membership changed", "conflict", func(h *creationHostFake, s *autoStoreFake, p *autoProviderFake) {
			h.p.GroupFingerprint = "changed-members"
		}},
		{"group changed", "conflict", func(h *creationHostFake, s *autoStoreFake, p *autoProviderFake) { h.p.Issue.ID = uuid.NewString() }},
		{"ignored source", "blocked", func(h *creationHostFake, s *autoStoreFake, p *autoProviderFake) { h.p.Issue.State = "ignored" }},
		{"provider not planned", "blocked", func(h *creationHostFake, s *autoStoreFake, p *autoProviderFake) {
			p.readErr = issuetracker.ErrUnsupportedStatus
		}},
		{"source changed after uncertain dispatch", "conflict", func(h *creationHostFake, s *autoStoreFake, p *autoProviderFake) { s.r.Policy.PendingState = "resolved" }},
		{"disable during credential read", "blocked", func(h *creationHostFake, s *autoStoreFake, p *autoProviderFake) {
			h.tokenHook = func() { s.r.Policy.Enabled = false }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, host, store, provider := autoFixture()
			tc.change(host, store, provider)
			out := AutoSyncStep(t.Context(), service, store, store.r)
			if out.Policy.Outcome != tc.want || provider.writes != 0 {
				t.Fatal(out.Policy, provider.writes)
			}
		})
	}
}
func TestAutoSyncUsesProviderSnapshotBeforeWriteAndRequiresAlignedOptIn(t *testing.T) {
	service, host, store, provider := autoFixture()
	snapshot := result(host.p, provider.status)
	in := api.UpdateIssueTrackerAutoSyncRequest{Enabled: true, ExpectedRevision: 1, ExpectedStatus: &snapshot, ExpectedIssueState: "open"}
	if _, err := service.EnableAutoSync(t.Context(), store, host.p.Issue.ID, in); err != nil {
		t.Fatal(err)
	}
	snapshot.RepositoryID++
	if _, err := service.EnableAutoSync(t.Context(), store, host.p.Issue.ID, in); err == nil {
		t.Fatal("stale native snapshot enabled")
	}
	service, host, store, provider = autoFixture()
	provider.status.State = "resolved"
	snapshot = result(host.p, provider.status)
	in.ExpectedStatus = &snapshot
	if _, err := service.EnableAutoSync(t.Context(), store, host.p.Issue.ID, in); err == nil {
		t.Fatal("unaligned lifecycle enabled")
	}
	service, host, store, provider = autoFixture()
	host.p.Issue.State = "resolved"
	provider.beforeSet = func() { provider.status.ID = "43" }
	out := AutoSyncStep(t.Context(), service, store, store.r)
	if out.Policy.Outcome != "conflict" || provider.writes != 0 {
		t.Fatal("changed provider snapshot overwritten", out.Policy)
	}
}

func TestGroupScopeTracksMembersAndSplitRulesWithoutTrafficNoise(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	detail := api.IssueDetail{Issue: api.Issue{ID: a, Fingerprint: "fingerprint", GroupingVersion: 1}, GroupMembers: []api.Issue{{ID: a}, {ID: b}}, SplitRules: []api.IssueSplitRule{{ID: uuid.NewString(), Enabled: true, UpdatedAt: now}}}
	baseline := GroupScopeFingerprint(detail)
	detail.Issue.State = "resolved"
	detail.Issue.EventCount = 100
	detail.GroupMembers[0].State = "resolved"
	detail.GroupMembers[0], detail.GroupMembers[1] = detail.GroupMembers[1], detail.GroupMembers[0]
	if GroupScopeFingerprint(detail) != baseline {
		t.Fatal("lifecycle, traffic or ordering changed source scope")
	}
	detail.GroupMembers = nil
	if GroupScopeFingerprint(detail) == baseline {
		t.Fatal("member removal did not change source scope")
	}
	detail.GroupMembers = []api.Issue{{ID: a}, {ID: b}}
	detail.SplitRules[0].Enabled = false
	if GroupScopeFingerprint(detail) == baseline {
		t.Fatal("split-rule edit did not change source scope")
	}
}

func TestGitHubPendingWriteIsObservedWithoutRedispatch(t *testing.T) {
	service, host, store, provider := autoFixture()
	host.p.Issue.State = "resolved"
	store.r.Policy.PendingState = "resolved"
	store.r.Policy.Outcome = "error"
	for range 3 {
		out := AutoSyncStep(t.Context(), service, store, store.r)
		if out.Policy.Outcome != "error" || out.Policy.Reason != "pending_outcome_unconfirmed" || out.Policy.PendingState != "resolved" || out.Baseline.State != "open" || provider.writes != 0 || store.pending != 0 {
			t.Fatal(out, provider.writes)
		}
		store.r = out
	}
	provider.status.State = "resolved"
	out := AutoSyncStep(t.Context(), service, store, store.r)
	if out.Policy.Outcome != "applied" || out.Policy.PendingState != "" || provider.writes != 0 {
		t.Fatal(out, provider.writes)
	}
	if reason := InspectionFailureReason(errors.New("token=secret-provider-body")); reason != "provider_unavailable" {
		t.Fatal(reason)
	}
}
