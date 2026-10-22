package apidserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 20261022120744 — the release page's timeline.

func releaseTimeline(t *testing.T, f approvalFixture) api.ReleaseTimeline {
	t.Helper()
	var out api.ReleaseTimeline
	if rec := guardDo(t, f.e, http.MethodGet, "/v1/deployments/"+f.canary.ID+"/timeline", nil, &out); rec.Code != http.StatusOK {
		t.Fatalf("timeline = %d %s", rec.Code, rec.Body)
	}
	return out
}

// timelineEvent returns the first event of a kind.
func timelineEvent(timeline api.ReleaseTimeline, kind string) (api.ReleaseTimelineEvent, bool) {
	for _, e := range timeline.Events {
		if e.Kind == kind {
			return e, true
		}
	}
	return api.ReleaseTimelineEvent{}, false
}

func TestReleaseTimelineFollowsACanaryThroughApprovalAndAdvance(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "timeline-app", time.Now().Add(-time.Hour), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1})
	client := f.worker(ctx, t)
	if _, err := client.AdvanceCanaryWithDecision(ctx, f.canary.ID, 0, healthyAdvance(f.prior.ID)); problemCodeOf(err) != api.CodeReleaseApprovalRequired {
		t.Fatalf("worker advance err = %v, want release_approval_required", err)
	}
	waiting := releaseTimeline(t, f)
	if waiting.Phase != api.ReleasePhaseAwaitingApproval || waiting.Summary != "Waiting for 1 more approval" || waiting.App != "timeline-app" ||
		waiting.StableDeploymentID != f.prior.ID || waiting.Approval == nil || waiting.Approval.State != api.ReleaseApprovalStateWaiting {
		t.Fatalf("waiting timeline = %+v", waiting)
	}
	if held, ok := timelineEvent(waiting, api.ReleaseEventHeld); !ok || held.Decision == nil || held.Decision.Rule != api.RolloutRuleHoldAwaitingApproval || held.Actor != "Gregale" {
		t.Fatalf("held event = %+v (events %+v)", held, waiting.Events)
	}
	_, requested := timelineEvent(waiting, api.ReleaseEventApprovalRequested)
	if _, created := timelineEvent(waiting, api.ReleaseEventCreated); !requested || !created {
		t.Fatalf("events = %+v", waiting.Events)
	}

	if rec := f.e.Do(t, http.MethodPost, "/v1/deployments/"+f.canary.ID+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove, Comment: "looks clean"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}
	if _, err := client.AdvanceCanaryWithDecision(ctx, f.canary.ID, 0, healthyAdvance(f.prior.ID)); err != nil {
		t.Fatalf("worker advance after approval: %v", err)
	}
	rolling := releaseTimeline(t, f)
	if rolling.Phase != api.ReleasePhaseRollingOut || rolling.CanaryStep != 1 || !strings.HasPrefix(rolling.Summary, "Rolling out: step 2 of 4, ") {
		t.Fatalf("rolling timeline = %+v", rolling)
	}
	approved, ok := timelineEvent(rolling, api.ReleaseEventApproved)
	if !ok || !strings.HasSuffix(approved.Summary, ": looks clean") || approved.Actor == "" {
		t.Fatalf("approved event = %+v", approved)
	}
	advanced, ok := timelineEvent(rolling, api.ReleaseEventAdvanced)
	if !ok || advanced.Decision == nil || advanced.Decision.Rule != api.RolloutRuleAdvanceHealthy || advanced.Actor != "Gregale" || advanced.TrafficPercent == nil {
		t.Fatalf("advanced event = %+v", advanced)
	}
	for i := 1; i < len(rolling.Events); i++ {
		if rolling.Events[i].At < rolling.Events[i-1].At {
			t.Fatalf("events out of order: %+v", rolling.Events)
		}
	}
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodGet, "/v1/deployments/3f9c1e2a-0b7d-4c55-9e1a-2d6f8b4c7e10/timeline", nil, nil),
		http.StatusNotFound, api.CodeNotFound)
}

func TestReleaseTimelineExplainsARejectedRelease(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "timeline-reject", time.Now().Add(-time.Hour), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1})
	if rec := f.e.Do(t, http.MethodPost, "/v1/deployments/"+f.canary.ID+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionReject, Comment: "checkout latency is up"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("reject = %d %s", rec.Code, rec.Body)
	}
	got := releaseTimeline(t, f)
	if got.Phase != api.ReleasePhaseRolledBack || !strings.HasPrefix(got.Summary, "Rolled back: ") {
		t.Fatalf("timeline = %+v", got)
	}
	if rejected, ok := timelineEvent(got, api.ReleaseEventRejected); !ok || !strings.HasSuffix(rejected.Summary, ": checkout latency is up") {
		t.Fatalf("rejected event = %+v (events %+v)", rejected, got.Events)
	}
	if rolledBack, ok := timelineEvent(got, api.ReleaseEventRolledBack); !ok || rolledBack.Decision == nil || rolledBack.Decision.Rule != api.RolloutRuleAbortApprovalReject {
		t.Fatalf("rolled back event = %+v (events %+v)", rolledBack, got.Events)
	}
}

func TestReleasePageRendersWithoutReleaseData(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := "3f9c1e2a-0b7d-4c55-9e1a-2d6f8b4c7e10"
	req := httptest.NewRequest(http.MethodGet, "/dashboard/releases/"+id, nil)
	req.SetPathValue("id", id)
	req = req.WithContext(apidserver.WithAccount(req.Context(), *e.Acct()))
	rec := httptest.NewRecorder()
	(*e.S()).RenderReleasePage(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `id="deployment" value="`+id+`"`) || !strings.Contains(body, "'/timeline'") ||
		!strings.Contains(body, "/rollouts/recover") || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("page = %d %v\n%s", rec.Code, rec.Header(), body)
	}
	bad := httptest.NewRequest(http.MethodGet, "/dashboard/releases/not-a-uuid", nil)
	bad.SetPathValue("id", "not-a-uuid")
	bad = bad.WithContext(apidserver.WithAccount(bad.Context(), *e.Acct()))
	rec = httptest.NewRecorder()
	(*e.S()).RenderReleasePage(rec, bad)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad id = %d", rec.Code)
	}
}
