package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020085706 — release approvals hold a canary until people approve.

type approvalFixture struct {
	e      apidserver.TestEnv
	app    state.App
	prior  state.Deployment
	canary state.Deployment
}

// newApprovalFixture sets the policy, then starts a canary that entered its
// first stage at stepStarted, so the release is created after the policy.
func newApprovalFixture(ctx context.Context, t *testing.T, slug string, stepStarted time.Time, policy api.SetReleaseApprovalPolicyRequest) approvalFixture {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:prior"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPut, "/v1/apps/"+slug+"/release-approval-policy", policy, nil); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d %s", rec.Code, rec.Body)
	}
	canary, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:canary", RootfsKey: "rootfs/canary", CanaryPreset: "balanced", CanaryStep: 0, CanaryTotalSteps: 4,
		RolloutState: "pending", TrafficPercent: 1, CreatedAt: time.Now().UTC().Add(time.Second),
		DeployedByUserID: (*e.Acct()).ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentCanaryState(ctx, canary.ID, "balanced", 0, 4, stepStarted, "rolling_out"); err != nil {
		t.Fatal(err)
	}
	return approvalFixture{e: e, app: app, prior: prior, canary: canary}
}

func (f approvalFixture) deployment(t *testing.T) api.DeploymentResponse {
	t.Helper()
	var out api.DeploymentResponse
	if rec := guardDo(t, f.e, http.MethodGet, "/v1/deployments/"+f.canary.ID, nil, &out); rec.Code != http.StatusOK {
		t.Fatalf("get deployment = %d %s", rec.Code, rec.Body)
	}
	return out
}

// worker mounts the Safe Deploy loopback routes and returns meterd's client.
func (f approvalFixture) worker(ctx context.Context, t *testing.T) *api.InternalSafeDeployClient {
	t.Helper()
	canaryToken := "canary-service-secret-0000000000000002"
	actionToken := "action-service-secret-0000000000000002"
	mux := http.NewServeMux()
	if err := (*f.e.S()).MountInternalSafeDeploy(mux, "127.0.0.1:9102", canaryToken, actionToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if err := (*f.e.Store()).StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	return api.NewInternalSafeDeployClient(server.URL, canaryToken, actionToken)
}

func healthyAdvance(stableID string) api.RolloutDecision {
	now := time.Now().UTC()
	return api.RolloutDecision{Kind: api.RolloutDecisionAdvance, Rule: api.RolloutRuleAdvanceHealthy, Step: 0, TrafficPercent: 1,
		WindowStart: now.Add(-time.Minute).Format(time.RFC3339Nano), WindowEnd: now.Format(time.RFC3339Nano), StableDeploymentID: stableID,
		Candidate: &api.RolloutDecisionSample{Requests: 40}, Stable: &api.RolloutDecisionSample{Requests: 40}, DecidedAt: now.Format(time.RFC3339Nano)}
}

func problemCodeOf(err error) string {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Problem.Code
	}
	return ""
}

func TestReleaseApprovalPolicyRoundTripAndValidation(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	if _, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "policy-app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "instant-app",
		Manifest: state.AppManifest{ReleasePolicy: api.ReleasePolicyImmediate}}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/policy-app/release-approval-policy"
	rec := e.Do(t, http.MethodGet, path, nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusNotFound, api.CodeReleaseApprovalPolicyNotFound)
	for _, bad := range []api.SetReleaseApprovalPolicyRequest{
		{RequiredApprovals: 0}, {RequiredApprovals: 6}, {RequiredApprovals: 1, AfterStage: 11},
		{RequiredApprovals: 1, TimeoutMinutes: 5}, {RequiredApprovals: 1, Approvers: "viewer"},
	} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodPut, path, bad, nil), http.StatusBadRequest, api.CodeValidation)
	}
	var policy api.ReleaseApprovalPolicy
	if rec := guardDo(t, e, http.MethodPut, path, api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 2}, &policy); rec.Code != http.StatusOK {
		t.Fatalf("set = %d %s", rec.Code, rec.Body)
	}
	// An app outside an organization has one person, whose approval counts.
	if policy.RequiredApprovals != 2 || policy.AfterStage != 1 || policy.TimeoutMinutes != 1440 || policy.Approvers != api.ReleaseApproversAdmin ||
		!policy.AllowSelfApproval || policy.UpdatedBy == "" {
		t.Fatalf("policy = %+v", policy)
	}
	var got api.ReleaseApprovalPolicy
	if rec := guardDo(t, e, http.MethodGet, path, nil, &got); rec.Code != http.StatusOK || !reflect.DeepEqual(got, policy) {
		t.Fatalf("get = %d %+v, want %+v", rec.Code, got, policy)
	}
	rec = e.Do(t, http.MethodPut, "/v1/apps/instant-app/release-approval-policy", api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeConflict)
	if rec := e.Do(t, http.MethodDelete, path, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodDelete, path, nil, nil), http.StatusNotFound, api.CodeReleaseApprovalPolicyNotFound)
}

func TestReleaseApprovalHoldsTheCanaryUntilApproved(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "approval-hold", time.Now().Add(-time.Hour), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1})
	id := f.canary.ID

	// Every path that would give the waiting release more traffic is refused.
	rec := f.e.Do(t, http.MethodPost, "/v1/deployments/"+id+"/canary/advance", api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeReleaseApprovalRequired)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.ReleaseApproval == nil ||
		problem.ReleaseApproval.State != api.ReleaseApprovalStateWaiting || problem.ReleaseApproval.Remaining() != 1 {
		t.Fatalf("problem = %+v, %v", problem, err)
	}
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodPatch, "/v1/deployments/"+id+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 50}, nil),
		http.StatusConflict, api.CodeReleaseApprovalRequired)
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodPost, "/v1/deployments/"+id+"/promote", map[string]any{}, nil),
		http.StatusConflict, api.CodeReleaseApprovalRequired)
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodPost, "/v1/apps/approval-hold/rollouts/recover", api.RecoverRolloutRequest{Action: "promote", Reason: "skip"}, nil),
		http.StatusConflict, api.CodeReleaseApprovalRequired)

	var pending api.ListReleaseApprovalsResponse
	if rec := guardDo(t, f.e, http.MethodGet, "/v1/apps/approval-hold/release-approvals", nil, &pending); rec.Code != http.StatusOK ||
		len(pending.Items) != 1 || pending.Items[0].DeploymentID != id || pending.Items[0].ExpiresAt == "" {
		t.Fatalf("pending = %d %+v", rec.Code, pending)
	}

	// meterd's tick is refused too, and apid records why the release waits.
	client := f.worker(ctx, t)
	if _, err := client.AdvanceCanaryWithDecision(ctx, id, 0, healthyAdvance(f.prior.ID)); problemCodeOf(err) != api.CodeReleaseApprovalRequired {
		t.Fatalf("worker advance err = %v, want release_approval_required", err)
	}
	if hold := f.deployment(t).RolloutHold; hold == nil || hold.Rule != api.RolloutRuleHoldAwaitingApproval || hold.Summary != "waiting for 1 more approval(s)" {
		t.Fatalf("rollout_hold = %+v", hold)
	}

	var status api.ReleaseApprovalStatus
	rec = guardDo(t, f.e, http.MethodPost, "/v1/deployments/"+id+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove, Comment: "canary looks clean"}, &status)
	if rec.Code != http.StatusOK || status.State != api.ReleaseApprovalStateApproved || status.Approvals != 1 ||
		len(status.Decisions) != 1 || status.Decisions[0].Role != state.ReleaseApproverRolePersonal || !status.Decisions[0].Counted {
		t.Fatalf("approve = %d %+v", rec.Code, status)
	}
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodPost, "/v1/deployments/"+id+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove}, nil), http.StatusConflict, api.CodeReleaseApprovalDecided)

	advanced, err := client.AdvanceCanaryWithDecision(ctx, id, 0, healthyAdvance(f.prior.ID))
	if err != nil || advanced.Deployment.CanaryStep != 1 {
		t.Fatalf("worker advance after approval = %+v, %v", advanced.Deployment, err)
	}
	if rec := guardDo(t, f.e, http.MethodGet, "/v1/apps/approval-hold/release-approvals", nil, &pending); rec.Code != http.StatusOK || len(pending.Items) != 0 {
		t.Fatalf("pending after approval = %+v", pending)
	}
}

func TestReleaseApprovalRejectionAbortsTheRollout(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "approval-reject", time.Now().Add(-time.Hour), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 2})
	path := "/v1/deployments/" + f.canary.ID + "/approvals"
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodPost, path, api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionReject}, nil),
		http.StatusBadRequest, api.CodeValidation)
	var status api.ReleaseApprovalStatus
	rec := guardDo(t, f.e, http.MethodPost, path, api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionReject, Comment: "checkout latency is up"}, &status)
	if rec.Code != http.StatusOK || status.State != api.ReleaseApprovalStateRejected {
		t.Fatalf("reject = %d %+v", rec.Code, status)
	}
	got := f.deployment(t)
	if got.RolloutState != "aborted" || got.RolloutDecision == nil || got.RolloutDecision.Rule != api.RolloutRuleAbortApprovalReject {
		t.Fatalf("after rejection state=%q decision=%+v", got.RolloutState, got.RolloutDecision)
	}
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodPatch, "/v1/deployments/"+f.canary.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 100}, nil),
		http.StatusConflict, api.CodeReleaseApprovalRejected)
}

func TestReleaseApprovalTimeoutAbortsOnTheWorkersNextTick(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "approval-timeout", time.Now().Add(-2*time.Hour),
		api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1, TimeoutMinutes: 60})
	client := f.worker(ctx, t)
	if _, err := client.AdvanceCanaryWithDecision(ctx, f.canary.ID, 0, healthyAdvance(f.prior.ID)); problemCodeOf(err) != api.CodeReleaseApprovalRequired {
		t.Fatalf("worker advance err = %v, want release_approval_required", err)
	}
	got := f.deployment(t)
	if got.RolloutState != "aborted" || got.RolloutDecision == nil || got.RolloutDecision.Rule != api.RolloutRuleAbortApprovalTimeout {
		t.Fatalf("after timeout state=%q decision=%+v", got.RolloutState, got.RolloutDecision)
	}
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodPost, "/v1/deployments/"+f.canary.ID+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove}, nil), http.StatusConflict, api.CodeReleaseApprovalDecided)
}

func TestReleaseApprovalRefusesTheDeployersOwnApproval(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "approval-self", time.Now().Add(-time.Hour), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1})
	approvals := *f.e.Store()
	policy, err := approvals.ReleaseApprovalPolicy(ctx, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	// As in an organization: the deployer's own approval does not count.
	policy.AllowSelfApproval = false
	if _, err := approvals.SetReleaseApprovalPolicy(ctx, policy, time.Now()); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodPost, "/v1/deployments/"+f.canary.ID+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove}, nil), http.StatusForbidden, api.CodeReleaseApprovalSelf)
	// The deployer may still reject their own release.
	var status api.ReleaseApprovalStatus
	if rec := guardDo(t, f.e, http.MethodPost, "/v1/deployments/"+f.canary.ID+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionReject, Comment: "wrong branch"}, &status); rec.Code != http.StatusOK ||
		status.State != api.ReleaseApprovalStateRejected {
		t.Fatalf("self rejection = %d %+v", rec.Code, status)
	}
}

func TestReleaseApprovalsRequireACanaryAtReleaseTime(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "approval-canary"})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:prior"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPut, "/v1/apps/approval-canary/release-approval-policy", api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1}, nil); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d %s", rec.Code, rec.Body)
	}
	path := "/v1/apps/approval-canary/deployments"
	rec := e.Do(t, http.MethodPost, path, api.CreateDeploymentRequest{Image: imageRef('c'), Canary: &api.CanaryPresetSpec{Preset: "none"}}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeReleaseApprovalCanaryRequired)
	half := 50
	rec = e.Do(t, http.MethodPost, path, api.CreateDeploymentRequest{Image: imageRef('d'), TrafficPercent: &half}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeReleaseApprovalCanaryRequired)
	zero := 0
	rec = e.Do(t, http.MethodPost, path, api.CreateDeploymentRequest{Image: imageRef('e'), TrafficPercent: &zero}, nil)
	var p api.Problem
	if json.Unmarshal(rec.Body.Bytes(), &p) == nil && p.Code == api.CodeReleaseApprovalCanaryRequired {
		t.Fatalf("a dark deployment takes no traffic and must not need a canary: %s", rec.Body)
	}
}

// approvalChannel creates a notification channel on the fixture's account.
func approvalChannel(ctx context.Context, t *testing.T, e apidserver.TestEnv, name, kind string) state.NotificationChannel {
	t.Helper()
	in := state.NotificationChannel{AccountID: (*e.Acct()).ID, Name: name, Kind: kind, TargetSealed: []byte("sealed")}
	switch kind {
	case "email":
		in.Email, in.TargetSealed = name+"@example.com", nil
	case "pagerduty":
		in.PagerDutyRegion = "us"
	}
	c, err := (*e.Store()).CreateNotificationChannel(ctx, in, api.MaxNotificationChannelsPerAccount)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// approvalEvents lists the release's recorded notifications by event.
func approvalEvents(ctx context.Context, t *testing.T, f approvalFixture) map[string]alertchannels.ReleaseNotice {
	t.Helper()
	due, err := (*f.e.Store()).DueReleaseApprovalEvents(ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]alertchannels.ReleaseNotice{}
	for _, ev := range due {
		if ev.DeploymentID != f.canary.ID {
			continue
		}
		if _, dup := out[ev.Event]; dup {
			t.Fatalf("event %s recorded twice", ev.Event)
		}
		var n alertchannels.ReleaseNotice
		if err := json.Unmarshal(ev.Payload, &n); err != nil {
			t.Fatal(err)
		}
		out[ev.Event] = n
	}
	return out
}

func TestReleaseApprovalNotifyChannelsMustBeSlackOrEmail(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	if _, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "notify-app"}); err != nil {
		t.Fatal(err)
	}
	approvalChannel(ctx, t, e, "releases", "slack")
	approvalChannel(ctx, t, e, "leads", "email")
	approvalChannel(ctx, t, e, "pager", "pagerduty")
	path := "/v1/apps/notify-app/release-approval-policy"
	for _, bad := range [][]string{{"pager"}, {"nobody"}, {"releases", "releases"}} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodPut, path, api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1, NotifyChannels: bad}, nil),
			http.StatusBadRequest, api.CodeValidation)
	}
	var policy api.ReleaseApprovalPolicy
	if rec := guardDo(t, e, http.MethodPut, path, api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1, NotifyChannels: []string{"releases", "leads"}}, &policy); rec.Code != http.StatusOK ||
		len(policy.NotifyChannels) != 2 || policy.NotifyChannels[0] != "releases" {
		t.Fatalf("set = %d %+v", rec.Code, policy)
	}
	var got api.ReleaseApprovalPolicy
	if rec := guardDo(t, e, http.MethodGet, path, nil, &got); rec.Code != http.StatusOK || len(got.NotifyChannels) != 2 || got.NotifyChannels[1] != "leads" {
		t.Fatalf("get = %d %+v", rec.Code, got)
	}
}

func TestReleaseApprovalEventsAreRecordedOncePerTransition(t *testing.T) {
	ctx := context.Background()
	f := newApprovalFixture(ctx, t, "approval-notify", time.Now().Add(-time.Hour), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1})
	approvalChannel(ctx, t, f.e, "releases", "slack")
	if rec := f.e.Do(t, http.MethodPut, "/v1/apps/approval-notify/release-approval-policy",
		api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1, NotifyChannels: []string{"releases"}}, nil); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d %s", rec.Code, rec.Body)
	}
	client := f.worker(ctx, t)
	for range 2 {
		if _, err := client.AdvanceCanaryWithDecision(ctx, f.canary.ID, 0, healthyAdvance(f.prior.ID)); problemCodeOf(err) != api.CodeReleaseApprovalRequired {
			t.Fatalf("worker advance err = %v", err)
		}
	}
	events := approvalEvents(ctx, t, f)
	requested, ok := events[state.ReleaseApprovalEventRequested]
	if !ok || len(events) != 1 || requested.Event != alertchannels.EventApprovalRequested || requested.App != "approval-notify" ||
		requested.Required != 1 || requested.Approvals != 0 || requested.ExpiresAt == "" || requested.Stage != 1 || requested.TotalStages != 4 ||
		!strings.HasSuffix(requested.ReviewURL, "/dashboard/releases/"+f.canary.ID+"/approval") {
		t.Fatalf("events = %+v", events)
	}
	if rec := f.e.Do(t, http.MethodPost, "/v1/deployments/"+f.canary.ID+"/approvals",
		api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove, Comment: "looks clean"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}
	events = approvalEvents(ctx, t, f)
	if approved, ok := events[state.ReleaseApprovalEventApproved]; !ok || approved.Actor == "" || approved.Comment != "looks clean" || approved.Approvals != 1 {
		t.Fatalf("events after approval = %+v", events)
	}
}

func TestReleaseApprovalRejectionAndTimeoutAreRecorded(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		started time.Duration
		event   string
	}{
		{name: "rejected", started: -10 * time.Minute, event: state.ReleaseApprovalEventRejected},
		{name: "expired", started: -2 * time.Hour, event: state.ReleaseApprovalEventExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newApprovalFixture(ctx, t, "approval-"+tc.name, time.Now().Add(tc.started), api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 2, TimeoutMinutes: 60})
			approvalChannel(ctx, t, f.e, "leads", "email")
			if rec := f.e.Do(t, http.MethodPut, "/v1/apps/approval-"+tc.name+"/release-approval-policy",
				api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 2, TimeoutMinutes: 60, NotifyChannels: []string{"leads"}}, nil); rec.Code != http.StatusOK {
				t.Fatalf("set policy = %d %s", rec.Code, rec.Body)
			}
			if tc.event == state.ReleaseApprovalEventRejected {
				if rec := f.e.Do(t, http.MethodPost, "/v1/deployments/"+f.canary.ID+"/approvals",
					api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionReject, Comment: "latency is up"}, nil); rec.Code != http.StatusOK {
					t.Fatalf("reject = %d %s", rec.Code, rec.Body)
				}
			} else if _, err := f.worker(ctx, t).AdvanceCanaryWithDecision(ctx, f.canary.ID, 0, healthyAdvance(f.prior.ID)); problemCodeOf(err) != api.CodeReleaseApprovalRequired {
				t.Fatalf("worker advance err = %v", err)
			}
			events := approvalEvents(ctx, t, f)
			if n, ok := events[tc.event]; !ok || len(events) != 1 || (tc.event == state.ReleaseApprovalEventRejected && n.Comment != "latency is up") {
				t.Fatalf("events = %+v", events)
			}
		})
	}
}

func TestReleaseApprovalPageRendersWithoutReleaseData(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := "3f9c1e2a-0b7d-4c55-9e1a-2d6f8b4c7e10"
	req := httptest.NewRequest(http.MethodGet, "/dashboard/releases/"+id+"/approval?intent=approve", nil)
	req.SetPathValue("id", id)
	req = req.WithContext(apidserver.WithAccount(req.Context(), *e.Acct()))
	rec := httptest.NewRecorder()
	(*e.S()).RenderReleaseApprovalPage(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `value="`+id+`"`) || !strings.Contains(body, `id="intent" value="approve"`) ||
		!strings.Contains(body, "/v1/deployments/") || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("page = %d %v\n%s", rec.Code, rec.Header(), body)
	}
	bad := httptest.NewRequest(http.MethodGet, "/dashboard/releases/not-a-uuid/approval", nil)
	bad.SetPathValue("id", "not-a-uuid")
	bad = bad.WithContext(apidserver.WithAccount(bad.Context(), *e.Acct()))
	rec = httptest.NewRecorder()
	(*e.S()).RenderReleaseApprovalPage(rec, bad)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad id = %d", rec.Code)
	}
}
