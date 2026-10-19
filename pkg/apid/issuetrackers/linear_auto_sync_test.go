package issuetrackers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

type linearAutoFake struct {
	*workflowFake
	uncertain, noEffect bool
	store               *autoStoreFake
}

func (p *linearAutoFake) SetStatus(ctx context.Context, expected issuetracker.Status, token, wanted, selected string) (issuetracker.Status, error) {
	if p.store.r.Policy.PendingState != wanted {
		return issuetracker.Status{}, errors.New("intent was not durable before dispatch")
	}
	if p.noEffect {
		p.writes++
		return issuetracker.Status{}, errors.New("unknown outcome")
	}
	out, err := p.linearStatusFake.SetStatus(ctx, expected, token, wanted, selected)
	if p.uncertain {
		return issuetracker.Status{}, errors.New("unknown outcome")
	}
	return out, err
}
func linearAutoFixture() (LinearService, *creationHostFake, *autoStoreFake, *linearAutoFake) {
	service, host, status := linearStatusFixture()
	status.status.State = "open"
	mapping := LinearWorkflowMapping{OpenStateID: status.status.StateID, ResolvedStateID: uuid.NewString()}
	store := &autoStoreFake{r: state.IssueTrackerAutoSyncRecord{AppID: host.p.Issue.AppID, AccountID: host.p.Connection.AccountID, BindingFingerprint: autoSyncFingerprint(host.p), ClaimID: uuid.NewString(), ClaimUntil: time.Now().Add(time.Minute), Baseline: fromStatus(status.status), Policy: api.IssueTrackerAutoSync{LinkID: host.p.Link.ID, Enabled: true, Revision: 1, SourceIssueID: host.p.Issue.ID, SourceState: "open", Outcome: "matched", LinearOpenStateID: mapping.OpenStateID, LinearResolvedStateID: mapping.ResolvedStateID}}}
	provider := &linearAutoFake{workflowFake: &workflowFake{linearStatusFake: status, team: status.status.TeamID, mapping: mapping}, store: store}
	service.Provider = provider
	return service, host, store, provider
}
func TestLinearAutoSyncRecoveryUsesExactWorkflowAndDoesNotRedispatch(t *testing.T) {
	for _, noEffect := range []bool{false, true} {
		t.Run(map[bool]string{false: "applied", true: "unconfirmed"}[noEffect], func(t *testing.T) {
			service, host, store, provider := linearAutoFixture()
			host.p.Issue.State = "resolved"
			provider.uncertain = true
			provider.noEffect = noEffect
			out := LinearAutoSyncStep(t.Context(), service, store, store.r)
			if out.Policy.Outcome != "error" || out.Policy.PendingState != "resolved" || provider.writes != 1 {
				t.Fatal(out.Policy, provider.writes)
			}
			store.r = out
			out = LinearAutoSyncStep(t.Context(), service, store, store.r)
			if provider.writes != 1 {
				t.Fatal("uncertain mutation repeated")
			}
			if noEffect {
				if out.Policy.Reason != "pending_outcome_unconfirmed" || out.Policy.PendingState != "resolved" {
					t.Fatal(out.Policy)
				}
			} else {
				if out.Policy.Outcome != "applied" || out.Policy.PendingState != "" || out.Baseline.StateID != store.r.Policy.LinearResolvedStateID {
					t.Fatal(out.Policy)
				}
				store.r = out
				host.p.Issue.State = "open"
				provider.uncertain = false
				out = LinearAutoSyncStep(t.Context(), service, store, store.r)
				if out.Policy.Outcome != "applied" || out.Baseline.StateID != store.r.Policy.LinearOpenStateID || provider.writes != 2 {
					t.Fatal(out.Policy)
				}
			}
		})
	}
}
func TestLinearAutoSyncFencesConnectionGroupAndManualWorkflowChanges(t *testing.T) {
	for _, kind := range []string{"revision", "group", "remote workflow", "foreign pending workflow", "disable", "ignored"} {
		t.Run(kind, func(t *testing.T) {
			service, host, store, provider := linearAutoFixture()
			want := "conflict"
			switch kind {
			case "revision":
				host.p.Connection.Linear.Revision++
			case "group":
				host.p.GroupFingerprint = strings.Repeat("b", 64)
			case "remote workflow":
				provider.status.StateID = uuid.NewString()
			case "foreign pending workflow":
				store.r.Policy.PendingState = "resolved"
				provider.status.State = "resolved"
				provider.status.StateID = uuid.NewString()
			case "disable":
				host.tokenHook = func() { store.r.Policy.Enabled = false }
				want = "blocked"
			case "ignored":
				host.p.Issue.State = "ignored"
				want = "blocked"
			}
			out := LinearAutoSyncStep(t.Context(), service, store, store.r)
			if out.Policy.Outcome != want || provider.writes != 0 {
				t.Fatal(out.Policy, provider.writes)
			}
		})
	}
}
func TestLinearAutoSyncEnableRequiresAlignedVerifiedSnapshot(t *testing.T) {
	for _, kind := range []string{"valid", "unaligned", "stale", "invalid workflow", "ambiguous provider"} {
		t.Run(kind, func(t *testing.T) {
			service, host, store, provider := linearAutoFixture()
			observed, err := service.Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			in := api.UpdateIssueTrackerAutoSyncRequest{Enabled: true, ExpectedRevision: 1, ExpectedLinearStatus: &observed, ExpectedIssueState: "open", LinearOpenStateID: store.r.Policy.LinearOpenStateID, LinearResolvedStateID: store.r.Policy.LinearResolvedStateID}
			switch kind {
			case "unaligned":
				host.p.Issue.State = "resolved"
				in.ExpectedIssueState = "resolved"
			case "stale":
				observed.ConnectionRevision++
			case "invalid workflow":
				provider.err = errors.New("private diagnostic")
			case "ambiguous provider":
				in.ExpectedStatus = &api.NativeIssueTrackerStatus{}
			}
			out, err := service.EnableAutoSync(t.Context(), store, host.p.Issue.ID, in)
			if kind == "valid" {
				if err != nil || out.Revision != 2 || out.LinearResolvedStateID != in.LinearResolvedStateID {
					t.Fatal(out, err)
				}
			} else if err == nil {
				t.Fatal("invalid delegation accepted")
			}
			if provider.writes != 0 {
				t.Fatal("enable mutated Linear")
			}
		})
	}
}
