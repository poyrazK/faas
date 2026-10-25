package apidserver_test

// Tests for the app.warm_snapshot_disabled audit emit
// (issue #470 / PR C / ADR-074 §3.2). The handler writes the row
// only on the true→false flip; tests cover both branches.
//
// setup()'s seeded app defaults WarmSnapshotEnabled=false, so to
// exercise the true→false flip we PATCH true first (TestUpdateAppWarmSnapshot_ProHappy
// already locks the round-trip) then PATCH false on the second
// request.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedWarmEnabledApp creates an app with WarmSnapshotEnabled=true
// already set. mustSeedApp leaves it false (default), so the
// audit emit's true→false flip precondition needs a starting
// state of true.
func seedWarmEnabledApp(t *testing.T, e apidserver.TestEnv, slug string) {
	t.Helper()
	app, err := (*e.Store()).CreateApp(context.Background(), state.App{
		AccountID:           (*e.Acct()).ID,
		Slug:                slug,
		Type:                state.AppTypeApp,
		Status:              state.AppActive,
		WarmSnapshotEnabled: true,
		MaxConcurrency:      1,
	})
	if err != nil {
		t.Fatalf("seed warm-enabled app %s: %v", slug, err)
	}
	_ = app
}

// TestUpdateApp_WarmSnapshotDisabledEmitsAudit locks the
// PR C / ADR-074 §3.2 emit: when PATCH warm_snapshot_enabled=false
// lands on an app that was previously true, the handler writes
// ONE app.warm_snapshot_disabled row (in addition to the standard
// app.updated row). Subject = &acct.ID per ADR-074.
func TestUpdateApp_WarmSnapshotDisabledEmitsAudit(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	seedWarmEnabledApp(t, e, "warm-disabled-emit")

	fals := false
	rec := e.Do(t, "PATCH", "/v1/apps/warm-disabled-emit",
		api.UpdateAppRequest{WarmSnapshotEnabled: &fals}, nil)
	if rec.Code != 200 {
		t.Fatalf("PATCH false: status %d: %s", rec.Code, rec.Body)
	}

	events, err := (*e.Store()).ListEvents(context.Background(), (*e.Acct()).ID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var updatedCount, disabledCount int
	for _, e := range events {
		switch e.Kind {
		case "app.updated":
			updatedCount++
		case "app.warm_snapshot_disabled":
			disabledCount++
		}
	}
	if updatedCount != 1 {
		t.Errorf("app.updated rows = %d, want 1", updatedCount)
	}
	if disabledCount != 1 {
		t.Errorf("app.warm_snapshot_disabled rows = %d, want 1", disabledCount)
	}

	// Payload of the disabled row must carry app_id + slug + old/new.
	for _, e := range events {
		if e.Kind != "app.warm_snapshot_disabled" {
			continue
		}
		var p struct {
			AppID string `json:"app_id"`
			Slug  string `json:"slug"`
			Old   bool   `json:"old"`
			New   bool   `json:"new"`
		}
		if err := json.Unmarshal(e.Data, &p); err != nil {
			t.Fatalf("payload unmarshal: %v (data=%s)", err, e.Data)
		}
		if p.AppID == "" {
			t.Errorf("payload app_id empty")
		}
		if p.Slug != "warm-disabled-emit" {
			t.Errorf("payload slug = %q, want warm-disabled-emit", p.Slug)
		}
		if !p.Old {
			t.Errorf("payload old = false, want true (pre-flip state)")
		}
		if p.New {
			t.Errorf("payload new = true, want false (post-flip state)")
		}
	}
}

// TestUpdateApp_WarmSnapshotDisabledNoEmit pins the negative
// branch: PATCH with WarmSnapshotEnabled UNSET (other fields
// changing) MUST NOT emit app.warm_snapshot_disabled — only the
// app.updated row lands. This locks the operator-no-intent-to-flip
// branch from ADR-074 §3.2.
func TestUpdateApp_WarmSnapshotDisabledNoEmit(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "warm-no-emit")

	// PATCH an unrelated field; WarmSnapshotEnabled stays nil.
	newRAM := 512
	rec := e.Do(t, "PATCH", "/v1/apps/warm-no-emit",
		api.UpdateAppRequest{RAMMB: &newRAM}, nil)
	if rec.Code != 200 {
		t.Fatalf("PATCH: status %d: %s", rec.Code, rec.Body)
	}

	events, err := (*e.Store()).ListEvents(context.Background(), (*e.Acct()).ID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var updatedCount, disabledCount int
	for _, e := range events {
		switch e.Kind {
		case "app.updated":
			updatedCount++
		case "app.warm_snapshot_disabled":
			disabledCount++
		}
	}
	if updatedCount != 1 {
		t.Errorf("app.updated rows = %d, want 1", updatedCount)
	}
	if disabledCount != 0 {
		t.Errorf("app.warm_snapshot_disabled rows = %d, want 0 (WarmSnapshotEnabled unset)", disabledCount)
	}
}

// TestUpdateApp_WarmSnapshotAlreadyFalseNoEmit locks the
// already-false branch: PATCH warm_snapshot_enabled=false on an
// app that was ALREADY false must NOT emit (no flip happened).
func TestUpdateApp_WarmSnapshotAlreadyFalseNoEmit(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "warm-already-false") // default WarmSnapshotEnabled=false

	fals := false
	rec := e.Do(t, "PATCH", "/v1/apps/warm-already-false",
		api.UpdateAppRequest{WarmSnapshotEnabled: &fals}, nil)
	if rec.Code != 200 {
		t.Fatalf("PATCH false: status %d: %s", rec.Code, rec.Body)
	}

	events, err := (*e.Store()).ListEvents(context.Background(), (*e.Acct()).ID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var disabledCount int
	for _, e := range events {
		if e.Kind == "app.warm_snapshot_disabled" {
			disabledCount++
		}
	}
	if disabledCount != 0 {
		t.Errorf("app.warm_snapshot_disabled rows = %d, want 0 (already false; no flip)", disabledCount)
	}
}
