package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

// stagedRelease seeds a release deployed with no traffic after f.release: live
// at an explicit 0%, never served.
func stagedRelease(t *testing.T, f postReleaseFixture) state.Deployment {
	t.Helper()
	store := *f.e.Store()
	d, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: f.release.AppID, ImageDigest: "sha256:" + apidserver.Repeat("f", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployLive, TrafficPercent: 0, TrafficPercentExplicit: true, CreatedAt: time.Now().UTC().Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := f.read(t, d.ID)
	if got.Status != state.DeployLive || got.TrafficPercent != 0 || !got.TrafficPercentExplicit || got.RolloutCompletedAt != nil {
		t.Fatalf("staged release = %s %d%% explicit=%v completed=%v, want live at an explicit 0%% that never served",
			got.Status, got.TrafficPercent, got.TrafficPercentExplicit, got.RolloutCompletedAt)
	}
	return got
}

// adr: 1445 amendment 5 — a newer release that never served (staged with
// --no-traffic here) does not stop the watch for the release that serves
// (release review 2026-11-02).
func TestPostReleaseWatchKeepsWatchingBesideAStagedRelease(t *testing.T) {
	f := newPostReleaseFixture(t, "prw-staged", true, time.Minute)
	stagedRelease(t, f)
	f.telemetry.windows[f.release.ID] = canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 700}
	f.sweep(t)
	if got := f.read(t, f.release.ID); got.LastAutoRollbackReason != string(state.AutoRollbackReasonPostReleaseRegression) {
		t.Fatalf("regressed release beside a staged one = %s %q, want it rolled back", got.Status, got.LastAutoRollbackReason)
	}
}

// adr: 20261022140000 amendment 1 — the break-glass pause holds a release
// built before it from taking its first traffic, and still lets a rollback
// to a release that served through.
func TestPlatformReleasePauseHoldsAReleaseThatNeverServed(t *testing.T) {
	f := newPostReleaseFixture(t, "pause-staged", false, time.Hour)
	staged := stagedRelease(t, f)
	pause := func(on bool) {
		t.Helper()
		if err := (*(*f.e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigPlatformReleasePause, json.RawMessage(strconv.FormatBool(on))); err != nil {
			t.Fatal(err)
		}
	}
	pause(true)
	// Promote meets the same guard (releaseApprovalTrafficProblem) after
	// checks this seeded release, which has no runtime image, does not pass.
	for name, call := range map[string]struct {
		method, path string
		body         any
	}{
		"traffic": {http.MethodPatch, "/v1/deployments/" + staged.ID + "/traffic", map[string]int{"traffic_percent": 100}},
		"rollback to it": {http.MethodPost, "/v1/apps/pause-staged/rollback",
			api.RollbackRequest{TargetDeploymentID: &staged.ID}},
	} {
		rec := guardDo(t, f.e, call.method, call.path, call.body, nil)
		if rec.Code != http.StatusServiceUnavailable || decodeProblemCode(rec.Body.Bytes()) != api.CodePlatformReleasePaused {
			t.Fatalf("%s of a never-served release while paused = %d %s, want 503 %s", name, rec.Code, rec.Body, api.CodePlatformReleasePaused)
		}
	}
	target := f.previous.ID
	rec := guardDo(t, f.e, http.MethodPost, "/v1/apps/pause-staged/rollback", api.RollbackRequest{TargetDeploymentID: &target}, nil)
	if decodeProblemCode(rec.Body.Bytes()) == api.CodePlatformReleasePaused {
		t.Fatalf("rollback to a release that served was paused: %d %s", rec.Code, rec.Body)
	}
	pause(false)
	rec = guardDo(t, f.e, http.MethodPatch, "/v1/deployments/"+staged.ID+"/traffic", map[string]int{"traffic_percent": 100}, nil)
	if decodeProblemCode(rec.Body.Bytes()) == api.CodePlatformReleasePaused {
		t.Fatalf("traffic after the pause was lifted = %d %s", rec.Code, rec.Body)
	}
}

// adr: 20261020085706 amendment 7 — the last channel of an approval policy
// cannot be deleted; once the policy names another, it can.
func TestDeletingTheLastChannelOfAnApprovalPolicyIsRefused(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	apidserver.MustSeedDeployment(t, e, "chan-app")
	req := withApprovers(ctx, t, e, api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1})
	if rec := guardDo(t, e, http.MethodPut, "/v1/apps/chan-app/release-approval-policy", req, nil); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d %s", rec.Code, rec.Body)
	}
	store := *e.Store()
	acct := *e.Acct()
	channels, err := store.ListNotificationChannels(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	var approvers string
	for _, c := range channels {
		if c.Name == "approvers" {
			approvers = c.ID
		}
	}
	rec := guardDo(t, e, http.MethodDelete, "/v1/notification-channels/"+approvers, nil, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "release approval policy of chan-app") {
		t.Fatalf("delete the policy's last channel = %d %s, want 409 naming the policy", rec.Code, rec.Body)
	}
	if _, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "leads", Kind: "email", Email: "leads@example.com"},
		api.MaxNotificationChannelsPerAccount); err != nil {
		t.Fatal(err)
	}
	req.NotifyChannels = []string{"approvers", "leads"}
	if rec := guardDo(t, e, http.MethodPut, "/v1/apps/chan-app/release-approval-policy", req, nil); rec.Code != http.StatusOK {
		t.Fatalf("name a second channel = %d %s", rec.Code, rec.Body)
	}
	if rec := guardDo(t, e, http.MethodDelete, "/v1/notification-channels/"+approvers, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete a channel the policy no longer depends on = %d %s, want 204", rec.Code, rec.Body)
	}
}
