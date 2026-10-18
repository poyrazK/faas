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

type directionalStoreFake struct {
	*autoStoreFake
	source  int64
	reopens int
	host    *creationHostFake
	fail    error
}

func (f *directionalStoreFake) GetIssueTrackerSourceRevision(context.Context, string, string, string) (int64, error) {
	return f.source, nil
}
func (f *directionalStoreFake) ConfigureIssueTrackerAutoSync(ctx context.Context, app, account, issue string, r state.IssueTrackerAutoSyncRecord, rev int64) (state.IssueTrackerAutoSyncRecord, error) {
	if r.Policy.Direction != "outbound" && r.SourceRevision != f.source {
		return r, state.IssueTrackerInboundChanged()
	}
	return f.autoStoreFake.ConfigureIssueTrackerAutoSync(ctx, app, account, issue, r, rev)
}
func (f *directionalStoreFake) ApplyIssueTrackerAutoReopen(ctx context.Context, r state.IssueTrackerAutoSyncRecord, o api.IssueTrackerProviderStatus) (state.IssueTrackerAutoSyncRecord, error) {
	if err := f.CheckIssueTrackerAutoSyncClaim(ctx, r); err != nil {
		return r, err
	}
	if r.SourceRevision != f.source {
		return r, state.IssueTrackerInboundChanged()
	}
	if f.fail != nil {
		return r, f.fail
	}
	f.reopens++
	f.source++
	f.host.p.Issue.State = "open"
	r.SourceRevision = f.source
	r.Policy.SourceState = "open"
	r.Baseline = o
	r.Policy.ObservedState = o.State
	r.Policy.Outcome = "applied"
	r.Policy.Reason = ""
	r.Completed = true
	r.ClaimID = ""
	f.r = r
	return r, nil
}
func directionalFixture(direction, life string) (Service, *creationHostFake, *directionalStoreFake, *autoProviderFake) {
	s, h, old, p := autoFixture()
	h.p.Issue.State = life
	old.r.Baseline.State = life
	old.r.Policy.SourceState = life
	old.r.Policy.Direction = direction
	old.r.SourceRevision = 7
	p.status.State = life
	return s, h, &directionalStoreFake{autoStoreFake: old, source: 7, host: h}, p
}
func TestAutomaticInboundReopenFencesIntentAndNeverResolves(t *testing.T) {
	for _, kind := range []string{"reopen", "both", "matched", "manual ABA", "manual lifecycle", "both edited", "read race", "regroup", "resolution", "missing baseline", "unsupported", "provider failure", "atomic failure"} {
		t.Run(kind, func(t *testing.T) {
			s, h, store, p := directionalFixture("inbound", "resolved")
			p.status.State = "open"
			want, reason := "applied", ""
			switch kind {
			case "both":
				store.r.Policy.Direction = "both"
			case "matched":
				p.status.State = "resolved"
				want = "matched"
			case "manual ABA":
				store.source++
				want, reason = "conflict", "source_intent_changed"
			case "manual lifecycle":
				h.p.Issue.State = "open"
				store.source++
				want, reason = "conflict", "source_intent_changed"
			case "both edited":
				store.r.Policy.Direction = "both"
				h.p.Issue.State = "open"
				store.source++
				want, reason = "conflict", "source_intent_changed"
			case "read race":
				h.tokenHook = func() { store.source++ }
				want, reason = "conflict", "source_intent_changed"
			case "regroup":
				h.p.GroupFingerprint += "changed"
				want, reason = "conflict", "source_connection_or_provider_changed"
			case "resolution":
				h.p.Issue.State = "open"
				store.r.Policy.SourceState = "open"
				store.r.Baseline.State = "open"
				p.status.State = "resolved"
				want, reason = "blocked", "fixing_deployment_required"
			case "missing baseline":
				store.r.SourceRevision = 0
				want, reason = "blocked", "source_revision_required"
			case "unsupported":
				p.readErr = issuetracker.ErrUnsupportedStatus
				want, reason = "blocked", "provider_state_unsupported"
			case "provider failure":
				p.readErr = errors.New("provider unavailable")
				want, reason = "error", "provider_unavailable"
			case "atomic failure":
				store.fail = state.IssueTrackerInboundChanged()
				want, reason = "conflict", "source_intent_changed"
			}
			out := AutoSyncStep(t.Context(), s, store, store.r)
			if out.Policy.Outcome != want || out.Policy.Reason != reason {
				t.Fatal(out.Policy)
			}
			if p.writes != 0 || store.pending != 0 {
				t.Fatal("inbound-only dispatched a provider write")
			}
			if want == "applied" {
				if store.reopens != 1 || !out.Completed || out.SourceRevision != store.source || out.Baseline.State != "open" {
					t.Fatal(out)
				}
				// A later poll sees its own baseline and cannot repeat the reopening.
				out.Completed = false
				out.ClaimID = uuid.NewString()
				store.r = out
				again := AutoSyncStep(t.Context(), s, store, out)
				if again.Policy.Outcome != "matched" || store.reopens != 1 {
					t.Fatal("sync loop", again.Policy)
				}
			} else if store.reopens != 0 {
				t.Fatal("unsafe local write")
			}
			if kind == "resolution" && (out.Baseline.State != "open" || out.Policy.ObservedState != "resolved") {
				t.Fatal("resolution lost its hold evidence", out)
			}
		})
	}
}
func TestBothDirectionsPersistsDispatchRevisionAndRecoversWithoutRedispatch(t *testing.T) {
	for _, kind := range []string{"confirmed", "unknown confirmed", "unknown unchanged", "edited after dispatch"} {
		t.Run(kind, func(t *testing.T) {
			s, h, store, p := directionalFixture("both", "open")
			h.p.Issue.State = "resolved"
			store.source = 8
			if kind != "confirmed" {
				p.uncertain = true
			}
			out := AutoSyncStep(t.Context(), s, store, store.r)
			if p.writes != 1 || store.pending != 1 {
				t.Fatal("local-only change was not pushed once", out.Policy)
			}
			if kind == "confirmed" {
				if out.Policy.Outcome != "applied" || out.SourceRevision != 8 || out.PendingSourceRevision != 0 {
					t.Fatal(out)
				}
				return
			}
			if out.PendingSourceRevision != 8 || out.SourceRevision != 7 || out.Policy.PendingState != "resolved" {
				t.Fatal("dispatch revision was not durable", out)
			}
			store.r = out
			if kind == "unknown unchanged" {
				p.status.State = "open"
			}
			if kind == "edited after dispatch" {
				store.source++
			}
			recovered := AutoSyncStep(t.Context(), s, store, out)
			if p.writes != 1 {
				t.Fatal("uncertain write was repeated")
			}
			switch kind {
			case "unknown confirmed":
				if recovered.Policy.Outcome != "applied" || recovered.SourceRevision != 8 || recovered.Policy.PendingState != "" {
					t.Fatal(recovered)
				}
			case "unknown unchanged":
				if recovered.Policy.Reason != "pending_outcome_unconfirmed" || recovered.Policy.PendingState != "resolved" {
					t.Fatal(recovered)
				}
			case "edited after dispatch":
				if recovered.Policy.Outcome != "conflict" || recovered.SourceRevision != 8 || recovered.Policy.PendingState != "" {
					t.Fatal(recovered)
				}
			}
		})
	}
}
func TestBothDirectionsDoesNotAcknowledgeEditsDuringProviderWrite(t *testing.T) {
	s, h, store, p := directionalFixture("both", "open")
	h.p.Issue.State = "resolved"
	store.source = 8
	p.beforeSet = func() { store.source++ }
	out := AutoSyncStep(t.Context(), s, store, store.r)
	if out.SourceRevision != 8 || store.source != 9 || out.Policy.Outcome != "applied" {
		t.Fatal(out)
	}
	store.r = out
	next := AutoSyncStep(t.Context(), s, store, out)
	if next.Policy.Outcome != "conflict" || next.Policy.Reason != "source_intent_changed" || p.writes != 1 {
		t.Fatal(next.Policy)
	}
}
func TestAutomaticLinearInboundUsesExactWorkflow(t *testing.T) {
	for _, mapped := range []bool{true, false} {
		t.Run(map[bool]string{true: "mapped", false: "unmapped"}[mapped], func(t *testing.T) {
			s, h, old, p := linearAutoFixture()
			h.p.Issue.State = "resolved"
			old.r.Policy.Direction = "inbound"
			old.r.Policy.SourceState = "resolved"
			old.r.Baseline.State = "resolved"
			old.r.Baseline.StateID = old.r.Policy.LinearResolvedStateID
			old.r.SourceRevision = 7
			p.status.State = "open"
			p.status.StateID = old.r.Policy.LinearOpenStateID
			if !mapped {
				p.status.StateID = uuid.NewString()
			}
			store := &directionalStoreFake{autoStoreFake: old, source: 7, host: h}
			out := LinearAutoSyncStep(t.Context(), s, store, old.r)
			if mapped {
				if out.Policy.Outcome != "applied" || store.reopens != 1 {
					t.Fatal(out.Policy)
				}
			} else if out.Policy.Reason != "provider_workflow_unmapped" || store.reopens != 0 {
				t.Fatal(out.Policy)
			}
			if p.writes != 0 {
				t.Fatal("inbound wrote provider")
			}
		})
	}
}
func TestEnableInboundDirectionPinsSourceDuringVerification(t *testing.T) {
	for _, race := range []bool{false, true} {
		t.Run(map[bool]string{false: "stable", true: "ABA"}[race], func(t *testing.T) {
			s, _, store, p := directionalFixture("inbound", "resolved")
			store.r.ClaimID = ""
			store.r.ClaimUntil = time.Time{}
			snapshot, err := s.Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			// Token acquisition occurs after the source revision is captured.
			if race {
				store.host.tokenHook = func() { store.source++ }
			}
			out, err := s.EnableAutoSync(t.Context(), store, store.r.Policy.SourceIssueID, api.UpdateIssueTrackerAutoSyncRequest{Direction: "inbound", Enabled: true, ExpectedRevision: 1, ExpectedIssueState: "resolved", ExpectedStatus: &snapshot})
			if race {
				if err == nil {
					t.Fatal("source change during verification delegated")
				}
			} else if err != nil || out.Direction != "inbound" || store.r.SourceRevision != 7 {
				t.Fatal(out, err)
			}
			if p.writes != 0 {
				t.Fatal("configuration wrote provider")
			}
		})
	}
}
