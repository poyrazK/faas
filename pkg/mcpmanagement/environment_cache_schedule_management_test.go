package mcpmanagement

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/cacheschedule"
)

func seedSchedule(t *testing.T, f *environmentCacheFixture, at time.Time) api.EnvironmentCacheSchedule {
	t.Helper()
	return seedScheduleContext(t.Context(), t, f, at)
}

func seedScheduleContext(ctx context.Context, t *testing.T, f *environmentCacheFixture, at time.Time) api.EnvironmentCacheSchedule {
	t.Helper()
	id := uuid.NewString()
	out, e := f.store.CreateEnvironmentCacheSchedule(ctx, f.account.ID, f.app.ID, "staging", api.CreateEnvironmentCacheScheduleRequest{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, ScheduleID: id, ExecuteAt: at, Purge: &api.CreateEnvironmentCachePurgeRequest{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, RequestID: id, Path: "/item"}})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func rescheduleArgs(id string) map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "schedule_id": id, "execute_at": time.Now().Add(2 * time.Hour).Format(time.RFC3339Nano), "reason": "Move maintenance window"}
}
func TestCacheScheduleMCPDiscoveryAndReschedule(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	at := time.Now().Add(time.Hour)
	a := seedSchedule(t, f, at)
	seedSchedule(t, f, at)
	seedSchedule(t, f, at)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "status": "scheduled", "limit": 1}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		out := callTool(t, ctx, cs, "gregale_list_environment_cache_schedules", args, false)
		var entries []api.EnvironmentCacheSchedule
		_ = json.Unmarshal(out["entries"], &entries)
		var cursor string
		_ = json.Unmarshal(out["next_cursor"], &cursor)
		if len(entries) != 1 || seen[entries[0].ScheduleID] {
			t.Fatal("discovery duplicates or missing entries")
		}
		seen[entries[0].ScheduleID] = true
		if i < 2 && cursor == "" || i == 2 && cursor != "" {
			t.Fatal("invalid discovery continuation")
		}
		args["after"] = cursor
	}
	review := callTool(t, ctx, cs, "gregale_plan_environment_cache_reschedule", rescheduleArgs(a.ScheduleID), false)
	var plan environmentReschedulePlan
	if json.Unmarshal(review["environment_cache_reschedule"], &plan) != nil || plan.Before.ScheduleID != a.ScheduleID || plan.Before.Purge.Path != "/item" {
		t.Fatal("replacement review incomplete")
	}
	if f.writes != 0 {
		t.Fatal("planning changed state")
	}
	id := planID(t, review)
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_reschedule", map[string]any{"plan_id": id}, false)
	var linked string
	_ = json.Unmarshal(out["replaces_schedule_id"], &linked)
	if linked != a.ScheduleID {
		t.Fatal("missing lineage")
	}
	callTool(t, ctx, cs, "gregale_apply_environment_cache_reschedule", map[string]any{"plan_id": id}, false)
	if f.writes != 1 {
		t.Fatal("duplicate replacement")
	}
	original, e := f.store.GetEnvironmentCacheSchedule(ctx, f.account.ID, f.app.ID, "staging", a.ScheduleID)
	if e != nil || original.Status != "cancelled" || original.ReplacedByScheduleID != plan.Proposed.ReplacementID {
		t.Fatal(original, e)
	}
	args["environment"] = "production"
	callTool(t, ctx, cs, "gregale_list_environment_cache_schedules", args, true)
}
func TestCacheScheduleMCPRescheduleRecoveryAndConsent(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	a := seedSchedule(t, f, time.Now().Add(time.Hour))
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "cache-reschedule"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	review := callTool(t, ctx, cs, "gregale_plan_environment_cache_reschedule", rescheduleArgs(a.ScheduleID), false)
	id := planID(t, review)
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_reschedule", map[string]any{"plan_id": id}, true)
	_ = cs.Close()
	if e := store.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := OpenStateStore(path, key)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_apply_environment_cache_reschedule", map[string]any{"plan_id": id}, false)
	var replacement string
	_ = json.Unmarshal(out["schedule_id"], &replacement)
	page, e := f.store.ListEnvironmentCacheSchedules(ctx, f.account.ID, f.app.ID, "staging", api.EnvironmentCacheScheduleListOptions{})
	if e != nil || len(page.Entries) != 2 {
		t.Fatal("recovery created multiple records", page, e)
	}
	_ = cs.Close()
	grant := opts.Environments.Named["shop/staging"]
	grant.Write = false
	opts.Environments.Named["shop/staging"] = grant
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	callTool(t, ctx, cs, "gregale_apply_environment_cache_reschedule", map[string]any{"plan_id": id}, true)
	callTool(t, ctx, cs, "gregale_list_environment_cache_schedules", map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "status": "scheduled"}, false)
	if replacement == a.ScheduleID || replacement == "" {
		t.Fatal("original UUID reused")
	}
}
func TestCacheScheduleMCPClaimInvalidatesRescheduleReview(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	a := seedSchedule(t, f, time.Now().Add(time.Second))
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_cache_reschedule", rescheduleArgs(a.ScheduleID), false))
	time.Sleep(time.Until(a.ExecuteAt) + 10*time.Millisecond)
	if worked, e := cacheschedule.RunOne(ctx, f.store); !worked || e != nil {
		t.Fatal(worked, e)
	}
	callTool(t, ctx, cs, "gregale_apply_environment_cache_reschedule", map[string]any{"plan_id": id}, true)
	page, e := f.store.ListEnvironmentCacheSchedules(ctx, f.account.ID, f.app.ID, "staging", api.EnvironmentCacheScheduleListOptions{})
	if e != nil || len(page.Entries) != 1 || page.Entries[0].Status != "executed" {
		t.Fatal("stale review replaced execution", page, e)
	}
}
