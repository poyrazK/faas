package main

// manifest_test.go — unit tests for deployManifestTriggers
// (issue #791 PR-C / ADR-090).
//
// Why not an httptest.Server: the helper only reads three methods
// (ListCrons / CreateCron / Whoami) from the SDK. The refactor in
// commands2.go introduced manifestCronClient for exactly this seam.
// Faking the three methods avoids the noise of routing every call
// through the SDK's full wire path; the API contract itself is
// covered by cmd/sdk-coverage's wire-level sweep.
//
// What we pin:
//   - empty dir → no-op (no error, no calls).
//   - manifest for a different app → no-op (filtered out).
//   - pre-count trip: manifest wants N, existing K, plan limit L; K+N>L → error, no CreateCron calls.
//   - happy path: 3 matching triggers, all → exactly 3 CreateCron calls in order with the right body.
//   - fail-fast at entry 4: 4th CreateCron errors with the exact summary block the operator sees.
//   - bad schedule in manifest → wrapped Validate error (no ListCrons / CreateCron).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeCronClient records every call and serves canned responses. The
// cronLimit + preExistingCrons shape lets the tests pin the
// pre-count branch without going through limits.go.
type fakeCronClient struct {
	preExistingCrons []api.CronResponse
	createErrAt      int                     // 0 = no error; N = error on the Nth CreateCron call
	createErr        error                   // error returned when createErrAt > 0
	createdCalls     []api.CreateCronRequest // every CreateCron invocation in order
	listErr          error                   // 0 = no error from ListCrons
	whoami           api.AccountResponse
	whoamiErr        error
}

func (f *fakeCronClient) ListCrons(_ context.Context, _ string) ([]api.CronResponse, error) {
	return f.preExistingCrons, f.listErr
}

func (f *fakeCronClient) CreateCron(_ context.Context, _ string, req api.CreateCronRequest) (api.CronResponse, error) {
	idx := len(f.createdCalls) + 1 // 1-based
	f.createdCalls = append(f.createdCalls, req)
	if f.createErrAt > 0 && idx == f.createErrAt {
		return api.CronResponse{}, f.createErr
	}
	return api.CronResponse{ID: "fake", Schedule: req.Schedule, Path: req.Path, Enabled: derefBool(req.Enabled)}, nil
}

func (f *fakeCronClient) Whoami(_ context.Context) (api.AccountResponse, error) {
	return f.whoami, f.whoamiErr
}

func derefBool(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

// writeManifest drops a gregale.yaml into dir with the given content.
// Caller controls cleanup via t.TempDir().
func writeManifest(t *testing.T, dir string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// writeGitkeep makes the dir a real directory in case the manifest
// load expects other tokens. Today Load only needs gregale.yaml,
// so this is just defensive.
func writeGitkeep(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), nil, 0o644); err != nil {
		t.Fatalf("write gitkeep: %v", err)
	}
}

func TestDeployManifestTriggers_NoManifestIsNoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fc := &fakeCronClient{whoami: api.AccountResponse{Plan: "pro"}}
	if err := deployManifestTriggers(context.Background(), fc, "my-api", dir); err != nil {
		t.Fatalf("err = %v, want nil (no manifest ⇒ no-op)", err)
	}
	if len(fc.createdCalls) != 0 {
		t.Errorf("CreateCron calls = %d, want 0", len(fc.createdCalls))
	}
}

func TestDeployManifestTriggers_ManifestForOtherAppIsNoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGitkeep(t, dir)
	writeManifest(t, dir, `triggers:
  - kind: cron
    app: other-app
    schedule: "0 3 * * *"
    path: /cleanup
`)
	fc := &fakeCronClient{whoami: api.AccountResponse{Plan: "pro"}}
	if err := deployManifestTriggers(context.Background(), fc, "my-api", dir); err != nil {
		t.Fatalf("err = %v, want nil (trigger for other-app should be filtered)", err)
	}
	if len(fc.createdCalls) != 0 {
		t.Errorf("CreateCron calls = %d, want 0 (filtered out)", len(fc.createdCalls))
	}
}

func TestDeployManifestTriggers_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGitkeep(t, dir)
	writeManifest(t, dir, `triggers:
  - kind: cron
    app: my-api
    schedule: "0 3 * * *"
    path: /cleanup
  - kind: cron
    app: my-api
    schedule: "*/5 * * * *"
    path: /tick
  - kind: cron
    app: my-api
    schedule: "0 0 * * 0"
    path: /weekly
    enabled: false
`)
	// Hobby plan = 5 crons/app; 0 existing → 3 fits with room.
	fc := &fakeCronClient{whoami: api.AccountResponse{Plan: "hobby"}}

	buf, restore := captureStdout(t)
	if err := deployManifestTriggers(context.Background(), fc, "my-api", dir); err != nil {
		restore()
		t.Fatalf("err = %v, want nil", err)
	}
	restore()

	if len(fc.createdCalls) != 3 {
		t.Fatalf("CreateCron calls = %d, want 3", len(fc.createdCalls))
	}
	// Order must match manifest order — a deploy with a
	// deterministic order makes idempotent re-runs cheaper to reason
	// about.
	for i, want := range []struct{ schedule, path string }{
		{"0 3 * * *", "/cleanup"},
		{"*/5 * * * *", "/tick"},
		{"0 0 * * 0", "/weekly"},
	} {
		if fc.createdCalls[i].Schedule != want.schedule {
			t.Errorf("CreateCron[%d].Schedule = %q, want %q", i, fc.createdCalls[i].Schedule, want.schedule)
		}
		if fc.createdCalls[i].Path != want.path {
			t.Errorf("CreateCron[%d].Path = %q, want %q", i, fc.createdCalls[i].Path, want.path)
		}
	}
	// Third trigger explicitly disables.
	if fc.createdCalls[2].Enabled == nil || *fc.createdCalls[2].Enabled {
		t.Errorf("CreateCron[2].Enabled = %v, want pointer to false", fc.createdCalls[2].Enabled)
	}
	// First two are enabled (mirror the manifest's absence of `enabled:`).
	if fc.createdCalls[0].Enabled == nil || !*fc.createdCalls[0].Enabled {
		t.Errorf("CreateCron[0].Enabled = %v, want pointer to true (nil pointer defaults enabled)", fc.createdCalls[0].Enabled)
	}
	if !strings.Contains(buf.String(), "3 trigger(s) applied") {
		t.Errorf("stdout missing success message; got %q", buf.String())
	}
}

func TestDeployManifestTriggers_PreCountTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGitkeep(t, dir)
	// 3 matching triggers.
	writeManifest(t, dir, `triggers:
  - {kind: cron, app: my-api, schedule: "0 3 * * *", path: /a}
  - {kind: cron, app: my-api, schedule: "0 4 * * *", path: /b}
  - {kind: cron, app: my-api, schedule: "0 5 * * *", path: /c}
`)
	// Hobby limit = 5, existing 4 → headroom 1 < wanted 3 → trip.
	fc := &fakeCronClient{
		preExistingCrons: make([]api.CronResponse, 4),
		whoami:           api.AccountResponse{Plan: "hobby"},
	}
	err := deployManifestTriggers(context.Background(), fc, "my-api", dir)
	if err == nil {
		t.Fatalf("err = nil, want pre-count trip error")
	}
	if !strings.Contains(err.Error(), "cron quota exceeded") {
		t.Errorf("err = %q, want pre-count trip copy", err)
	}
	if !strings.Contains(err.Error(), "plan allows 5") {
		t.Errorf("err = %q, want plan-limit number", err)
	}
	if len(fc.createdCalls) != 0 {
		t.Errorf("CreateCron calls = %d, want 0 (pre-count trip must not call CreateCron)", len(fc.createdCalls))
	}
}

func TestDeployManifestTriggers_FailFastAtEntry4(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGitkeep(t, dir)
	writeManifest(t, dir, `triggers:
  - {kind: cron, app: my-api, schedule: "0 3 * * *", path: /a}
  - {kind: cron, app: my-api, schedule: "0 4 * * *", path: /b}
  - {kind: cron, app: my-api, schedule: "0 5 * * *", path: /c}
  - {kind: cron, app: my-api, schedule: "0 6 * * *", path: /d}
  - {kind: cron, app: my-api, schedule: "0 7 * * *", path: /e}
`)
	fc := &fakeCronClient{
		whoami:      api.AccountResponse{Plan: "pro"}, // 20-limit
		createErrAt: 4,
		createErr:   errors.New("synthetic: 402 cron_quota_exceeded"),
	}
	err := deployManifestTriggers(context.Background(), fc, "my-api", dir)
	if err == nil {
		t.Fatal("err = nil, want fail-fast at entry 4")
	}
	// Exact summary text the operator sees:
	//   "trigger 4/5 (my-api "0 6 * * *" /d) rejected: synthetic: ...\n
	//    3 triggers created, 2 not attempted. ..."
	wantSubstrs := []string{
		"trigger 4/5",
		"my-api",
		"0 6 * * *",
		"/d",
		"synthetic: 402 cron_quota_exceeded",
		"3 triggers created, 2 not attempted",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("err missing substring %q; full=%q", s, err.Error())
		}
	}
	// Hard invariant: the helper must stop after the first failure.
	if len(fc.createdCalls) != 4 {
		t.Errorf("CreateCron calls = %d, want 4 (3 succeed + 1 fail-fast tripwire)", len(fc.createdCalls))
	}
}

func TestDeployManifestTriggers_BadScheduleSurfacesValidateError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGitkeep(t, dir)
	writeManifest(t, dir, `triggers:
  - {kind: cron, app: my-api, schedule: "not-a-cron", path: /a}
`)
	fc := &fakeCronClient{whoami: api.AccountResponse{Plan: "pro"}}
	err := deployManifestTriggers(context.Background(), fc, "my-api", dir)
	if err == nil {
		t.Fatal("err = nil, want gregalemanifest.Validate error")
	}
	if !strings.Contains(err.Error(), "bad schedule") {
		t.Errorf("err = %q, want bad-schedule copy", err)
	}
	// The pre-count must not have happened — Validate runs first.
	if len(fc.createdCalls) != 0 {
		t.Errorf("CreateCron calls = %d, want 0 (Validate ran first)", len(fc.createdCalls))
	}
}

func TestDeployManifestTriggers_WorkflowDefinitionsDoNotAffectTriggers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGitkeep(t, dir)
	writeManifest(t, dir, `workflows:
  - name: process_order
    trigger: {type: manual}
    steps:
      - name: charge
        run: charge_stripe
        input: {order_id: o-1}
        timeout: 30s
`)
	fc := &fakeCronClient{whoami: api.AccountResponse{Plan: "hobby"}}
	err := deployManifestTriggers(context.Background(), fc, "my-api", dir)
	if err != nil {
		t.Fatalf("error = %v, want no trigger fan-out error", err)
	}
	if len(fc.createdCalls) != 0 {
		t.Fatalf("CreateCron calls = %d, want 0 for workflow-only manifest", len(fc.createdCalls))
	}
}
