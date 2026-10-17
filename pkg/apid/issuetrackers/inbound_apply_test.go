package issuetrackers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type inboundStoreFake struct {
	guard int64
	calls int
	err   error
	last  state.IssueTrackerInboundApply
}

func (f *inboundStoreFake) GetIssueTrackerInboundGuard(context.Context, string, string) (int64, error) {
	return f.guard, nil
}
func (f *inboundStoreFake) ListIssueTrackerInboundDeployments(context.Context, string, string, string) ([]state.IssueTrackerFixingDeployment, error) {
	return nil, nil
}
func (f *inboundStoreFake) ApplyIssueTrackerInbound(_ context.Context, in state.IssueTrackerInboundApply) (api.Issue, error) {
	f.calls++
	if in.GuardRevision != f.guard {
		return api.Issue{}, state.IssueTrackerInboundChanged()
	}
	if f.err != nil {
		return api.Issue{}, f.err
	}
	f.last = in
	return api.Issue{ID: in.Record.Policy.SourceIssueID, State: in.Observed.State}, nil
}

func TestInboundPullFencesLocalAndProviderChanges(t *testing.T) {
	for _, kind := range []string{"resolve", "reopen", "missing deployment", "zero deployment", "unexpected deployment", "stale guard", "provider changed", "source changed", "change during read", "active claim", "pending write", "scope changed", "storage failure"} {
		t.Run(kind, func(t *testing.T) {
			s, host, policies, provider := autoFixture()
			r := policies.r
			r.ClaimUntil = time.Time{}
			provider.status.State = "resolved"
			f := &inboundStoreFake{guard: 7}
			in := InboundPull{GuardRevision: 7, Expected: fromStatus(provider.status), FixedDeploymentID: uuid.NewString()}
			switch kind {
			case "reopen":
				host.p.Issue.State = "resolved"
				r.Policy.SourceState = "resolved"
				r.Baseline.State = "resolved"
				provider.status.State = "open"
				in.Expected = fromStatus(provider.status)
				in.FixedDeploymentID = ""
			case "missing deployment":
				in.FixedDeploymentID = ""
			case "zero deployment":
				in.FixedDeploymentID = uuid.Nil.String()
			case "unexpected deployment":
				provider.status.State = "open"
				in.Expected = fromStatus(provider.status)
			case "stale guard":
				f.guard++
			case "provider changed":
				provider.status.State = "open"
			case "source changed":
				host.p.Issue.State = "resolved"
			case "change during read":
				host.tokenHook = func() { f.guard++ }
			case "active claim":
				r.ClaimUntil = time.Now().Add(time.Minute)
			case "pending write":
				r.Policy.PendingState = "resolved"
			case "scope changed":
				host.p.GroupFingerprint += "changed"
			case "storage failure":
				f.err = errors.New("storage unavailable")
			}
			out, err := s.Pull(t.Context(), f, r, in)
			if kind == "resolve" || kind == "reopen" {
				if err != nil || out.State != in.Expected.State || f.calls != 1 || f.last.FixedDeploymentID != in.FixedDeploymentID || f.last.Record.Policy.LinkID != r.Policy.LinkID {
					t.Fatal(out, err, f.calls)
				}
			} else if err == nil {
				t.Fatal("unsafe pull accepted", out)
			}
			if provider.writes != 0 || policies.pending != 0 {
				t.Fatal("pull wrote provider or outbound intent")
			}
			if kind == "stale guard" || kind == "missing deployment" || kind == "zero deployment" || kind == "active claim" || kind == "pending write" {
				if provider.reads != 0 || f.calls != 0 {
					t.Fatal("invalid request reached provider or local action")
				}
			}
		})
	}
}

func TestLinearInboundPullRequiresExactMappedProviderWorkflow(t *testing.T) {
	for _, mapped := range []bool{true, false} {
		t.Run(map[bool]string{true: "mapped", false: "unmapped"}[mapped], func(t *testing.T) {
			s, _, policies, provider := linearAutoFixture()
			r := policies.r
			r.ClaimUntil = time.Time{}
			provider.status.State = "resolved"
			provider.status.StateID = r.Policy.LinearResolvedStateID
			if !mapped {
				provider.status.StateID = uuid.NewString()
			}
			f := &inboundStoreFake{guard: 3}
			out, err := s.Pull(t.Context(), f, r, InboundPull{GuardRevision: 3, Expected: fromStatus(provider.status), FixedDeploymentID: uuid.NewString()})
			if mapped {
				if err != nil || out.State != "resolved" || f.calls != 1 {
					t.Fatal(out, err)
				}
			} else if err == nil || f.calls != 0 {
				t.Fatal("unmapped workflow applied")
			}
			if provider.writes != 0 {
				t.Fatal("inbound pull wrote Linear")
			}
		})
	}
}
