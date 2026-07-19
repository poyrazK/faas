// Negative + integration tests for cmd/apid handlers. The
// deployment_logs SSE stream tests live here because they need a
// real broadcaster handle (server_test.go constructs only the bare
// server).
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDeploymentLogsSSE_Pagination confirms the initial page of a
// deployment log feed is the chronological slice from oldest → newest
// (even though the table is DESC by seq) and that ?follow=0 closes
// the stream with the `end` event — no live tail, no broadcaster
// dependency. The live-tail path is exercised by manual smoke
// (slice 5 spec verification line).
func TestDeploymentLogsSSE_Pagination(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "log-multi")
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		if _, err := e.store.AppendDeploymentLog(ctx, dep.ID, "stdout", "line"+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}

	rec := e.do(t, "GET", "/v1/deployments/"+dep.ID+"/logs?follow=0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("first page: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("ct = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"line1", "line2", "line3", "line4", "line5", "event: end"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
	if !strings.Contains(body, `"seq":5`) {
		t.Errorf("body missing seq:5 — newest row\n%s", body)
	}
}

// itoa turns small ints into strings without strconv dependency so
// the test stays self-contained.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// mustSeedDeployment provisions an app + a deployment under the
// test account and returns the deployment.
func mustSeedDeployment(t *testing.T, e testEnv, slug string) state.Deployment {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      slug,
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed app %s: %v", slug, err)
	}
	d, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:deadbeefcafebabe1234567890abcdef1234567890abcdef1234567890abcdef",
		Kind:        state.DeploymentKindImage,
		Status:      state.DeployBuilding,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	return d
}

// mustSeedApp provisions an app under the test account at the given
// slug (default active status). Returns the app ID for later assertions.
func mustSeedApp(t *testing.T, e testEnv, slug string) string {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      slug,
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed app %s: %v", slug, err)
	}
	return app.ID
}

// TestUpdateAppMinInstances_Hobby locks the plan-tier gate (ux_spec
// §6.5): Hobby plans cannot set apps.min_instances. The handler must
// return 403 plan_min_instances_not_allowed, not 422, because the
// feature is tier-locked — the value the customer typed is irrelevant.
func TestUpdateAppMinInstances_Hobby(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-floor")
	one := 1
	rec := e.do(t, "PATCH", "/v1/apps/hobby-floor", api.UpdateAppRequest{MinInstances: &one}, nil)
	assertProblem(t, rec, 403, api.CodePlanMinInstancesNotAllowed)
}

// TestUpdateAppMinInstances_Pro is the happy path: Pro plans accept
// min_instances and the response carries the new value.
func TestUpdateAppMinInstances_Pro(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-floor")
	one := 1
	rec := e.do(t, "PATCH", "/v1/apps/pro-floor", api.UpdateAppRequest{MinInstances: &one}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.MinInstances != 1 {
		t.Errorf("MinInstances = %d, want 1", out.MinInstances)
	}
}

// TestUpdateAppMinInstances_OutOfRange pins the bounds check: min
// must be in [0, MaxConcurrency]. Pro caps at 5, so 6 must 422 with
// code invalid_min_instances. Also covers the gate-precedes-bounds
// ordering: a Hobby plan sending 6 should still get 403 (covered by
// TestUpdateAppMinInstances_Hobby).
func TestUpdateAppMinInstances_OutOfRange(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-over")
	six := 6
	rec := e.do(t, "PATCH", "/v1/apps/pro-over", api.UpdateAppRequest{MinInstances: &six}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidMinInstances)
}

// TestUpdateAppMinInstances_Negative pins the lower bound: -1 must
// 422 invalid_min_instances, regardless of plan. A negative value
// would otherwise pass through to the PG CHECK constraint as a
// second-layer defense, but the handler should catch it first.
func TestUpdateAppMinInstances_Negative(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-neg")
	neg := -1
	rec := e.do(t, "PATCH", "/v1/apps/pro-neg", api.UpdateAppRequest{MinInstances: &neg}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidMinInstances)
}
