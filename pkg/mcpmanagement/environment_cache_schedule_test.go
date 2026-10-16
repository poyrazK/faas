package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/cacheschedule"
)

func newEnvironmentScheduleFixture(t *testing.T) (*environmentCacheFixture, *httptest.Server, Options) {
	t.Helper()
	f, base, opts := newEnvironmentPurgeFixture(t)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/capabilities" {
			_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{EnvironmentCacheSchedules: cacheschedule.Capabilities(f.store)})
			return
		}

		if r.URL.Path == "/v1/apps/alpha/cache-policy/convergence" {
			out, err := f.store.GetEnvironmentCacheConvergence(r.Context(), f.account.ID, f.app.ID, r.URL.Query().Get("environment"))
			if err != nil {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		prefix := "/v1/apps/alpha/cache/schedules"
		if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
			base.Config.Handler.ServeHTTP(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		scope := r.URL.Query().Get("environment")

		if r.Method == http.MethodGet && r.URL.Path == prefix+"/activity/checkpoint" {
			point, e := f.store.GetEnvironmentCacheScheduleActivityCheckpoint(r.Context(), f.account.ID, f.app.ID, scope)
			if e != nil {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(point)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == prefix+"/activity" {
			_, after, limit, e := cacheschedule.ActivityQuery(r)
			if e != nil {
				w.WriteHeader(400)
				return
			}
			page, e := f.store.GetEnvironmentCacheScheduleActivity(r.Context(), f.account.ID, f.app.ID, scope, after, limit)
			if e != nil {
				if p := cacheschedule.ActivityProblem(e); p != nil {
					api.WriteProblem(w, p)
					return
				}
				api.WriteProblem(w, api.NewProblem(400, api.CodeConflict, "Invalid activity cursor", "Read the same immutable scope"))
				return
			}
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, prefix+"/")

		if r.Method == http.MethodGet && r.URL.Path == prefix {
			_, options, e := cacheschedule.ListQuery(r)
			if e != nil {
				w.WriteHeader(400)
				return
			}
			page, e := f.store.ListEnvironmentCacheSchedules(r.Context(), f.account.ID, f.app.ID, scope, options)
			if e != nil {
				api.WriteProblem(w, api.NewProblem(400, api.CodeConflict, "Invalid page", "Inspect query"))
				return
			}
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reschedule") {
			var req api.RescheduleEnvironmentCacheRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Idempotency-Key") != req.ReplacementID {
				w.WriteHeader(400)
				return
			}
			out, e := f.store.RescheduleEnvironmentCache(r.Context(), f.account.ID, f.app.ID, scope, strings.TrimSuffix(id, "/reschedule"), req)
			if e != nil {
				api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Schedule changed", "Inspect again"))
				return
			}
			f.writes++
			if f.ambiguous {
				f.ambiguous = false
				w.WriteHeader(504)
				return
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		var out api.EnvironmentCacheSchedule
		var err error
		switch r.Method {
		case "POST":
			var req api.CreateEnvironmentCacheScheduleRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Idempotency-Key") != req.ScheduleID {
				w.WriteHeader(400)
				return
			}
			out, err = f.store.CreateEnvironmentCacheSchedule(r.Context(), f.account.ID, f.app.ID, scope, req)
			f.writes++
			if err == nil && f.ambiguous {
				f.ambiguous = false
				w.WriteHeader(504)
				return
			}
		case "GET":
			out, err = f.store.GetEnvironmentCacheSchedule(r.Context(), f.account.ID, f.app.ID, scope, id)
		case "DELETE":
			out, err = f.store.CancelEnvironmentCacheSchedule(r.Context(), f.account.ID, f.app.ID, scope, id)
		default:
			w.WriteHeader(405)
			return
		}
		if err != nil {
			api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Schedule changed", "inspect schedule again"))
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(b.Close)
	opts.APIBase = b.URL
	return f, b, opts
}
func schedulePlanArgs() map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "execute_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "purge_path": "", "reason": "Maintenance window purge"}
}
func TestEnvironmentCacheScheduleMCPReviewCancel(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	review := callTool(t, ctx, cs, "gregale_plan_environment_cache_schedule", schedulePlanArgs(), false)
	var plan environmentSchedulePlan
	if json.Unmarshal(review["environment_cache_schedule"], &plan) != nil || plan.Proposed.Purge == nil || plan.Proposed.Purge.Path != "" || plan.Proposed.EnvironmentID != f.env.ID {
		t.Fatal("schedule review incomplete")
	}
	if f.writes != 0 {
		t.Fatal("review mutated intent")
	}
	id := planID(t, review)
	receipt := callTool(t, ctx, cs, "gregale_apply_environment_cache_schedule", map[string]any{"plan_id": id}, false)
	if string(receipt["status"]) != `"scheduled"` {
		t.Fatal("acceptance marked executed")
	}
	callTool(t, ctx, cs, "gregale_apply_environment_cache_schedule", map[string]any{"plan_id": id}, false)
	if f.writes != 1 {
		t.Fatal("cached apply duplicated schedule")
	}
	args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "schedule_id": plan.Proposed.ScheduleID}
	out := callTool(t, ctx, cs, "gregale_get_environment_cache_schedule", args, false)
	var value api.EnvironmentCacheSchedule
	if json.Unmarshal(out["schedule"], &value) != nil || value.Status != "scheduled" {
		t.Fatal("invalid status")
	}
	for i := 0; i < 2; i++ {
		out = callTool(t, ctx, cs, "gregale_cancel_environment_cache_schedule", args, false)
		if json.Unmarshal(out["schedule"], &value) != nil || value.Status != "cancelled" {
			t.Fatal("cancellation failed")
		}
	}
	args["environment"] = "production"
	callTool(t, ctx, cs, "gregale_get_environment_cache_schedule", args, true)
	for _, bad := range []map[string]any{{"rules": []any{}}, {"purge_path": "/item?x=1"}, {"execute_at": time.Now().Add(-time.Hour).Format(time.RFC3339)}} {
		a := schedulePlanArgs()
		for k, v := range bad {
			a[k] = v
		}
		callTool(t, ctx, cs, "gregale_plan_environment_cache_schedule", a, true)
	}
}
func TestEnvironmentCacheScheduleMCPExecutionEvidence(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	a := schedulePlanArgs()
	a["execute_at"] = time.Now().Add(time.Second).Format(time.RFC3339Nano)
	review := callTool(t, ctx, cs, "gregale_plan_environment_cache_schedule", a, false)
	var plan environmentSchedulePlan
	_ = json.Unmarshal(review["environment_cache_schedule"], &plan)
	callTool(t, ctx, cs, "gregale_apply_environment_cache_schedule", map[string]any{"plan_id": planID(t, review)}, false)
	time.Sleep(time.Until(plan.Proposed.ExecuteAt) + 10*time.Millisecond)
	if worked, err := cacheschedule.RunOne(ctx, f.store); !worked || err != nil {
		t.Fatal(worked, err)
	}
	out := callTool(t, ctx, cs, "gregale_get_environment_cache_schedule", map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "schedule_id": plan.Proposed.ScheduleID}, false)
	var value api.EnvironmentCacheSchedule
	_ = json.Unmarshal(out["schedule"], &value)
	var evidence api.EnvironmentCachePurgeStatus
	_ = json.Unmarshal(out["purge_status"], &evidence)
	if value.Status != "executed" || value.PurgeResult == nil || evidence.Status != "degraded" {
		t.Fatal("execution confused with convergence", value, evidence)
	}
}
func TestEnvironmentCacheScheduleMCPJournalRecovery(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "schedule"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	review := callTool(t, ctx, cs, "gregale_plan_environment_cache_schedule", schedulePlanArgs(), false)
	id := planID(t, review)
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_cache_schedule", map[string]any{"plan_id": id}, true)
	_ = cs.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	// A new MCP management instance restores the original review UUID.
	cs2, ctx2 := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	receipt := callTool(t, ctx2, cs2, "gregale_apply_environment_cache_schedule", map[string]any{"plan_id": id}, false)
	if string(receipt["status"]) != `"scheduled"` {
		t.Fatal("lost schedule reply not recovered")
	}
	_ = cs2.Close()
	grant := opts.Environments.Named["shop/staging"]
	grant.Write = false
	opts.Environments.Named["shop/staging"] = grant
	cs3, ctx3 := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	callTool(t, ctx3, cs3, "gregale_apply_environment_cache_schedule", map[string]any{"plan_id": id}, true)
}

func TestEnvironmentCacheScheduleMCPPolicySupersession(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	a := schedulePlanArgs()
	delete(a, "purge_path")
	a["rules"] = []any{}
	a["inherited"] = true
	a["execute_at"] = time.Now().Add(time.Second).Format(time.RFC3339Nano)
	review := callTool(t, ctx, cs, "gregale_plan_environment_cache_schedule", a, false)
	var plan environmentSchedulePlan
	_ = json.Unmarshal(review["environment_cache_schedule"], &plan)
	if plan.Proposed.Policy == nil || !plan.Proposed.Policy.Inherited || *plan.Proposed.Policy.ExpectedRevision != 0 {
		t.Fatal("policy review lacks exact guard")
	}
	callTool(t, ctx, cs, "gregale_apply_environment_cache_schedule", map[string]any{"plan_id": planID(t, review)}, false)
	time.Sleep(time.Until(plan.Proposed.ExecuteAt) + 10*time.Millisecond)
	if worked, err := cacheschedule.RunOne(ctx, f.store); !worked || err != nil {
		t.Fatal(worked, err)
	}
	args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "schedule_id": plan.Proposed.ScheduleID}
	out := callTool(t, ctx, cs, "gregale_get_environment_cache_schedule", args, false)
	var evidence api.EnvironmentCacheConvergence
	_ = json.Unmarshal(out["policy_convergence"], &evidence)
	if evidence.Status != "degraded" || evidence.Revision != 1 {
		t.Fatal("scheduled policy convergence", evidence)
	}
	edit := *plan.Proposed.Policy
	one := int64(1)
	edit.ExpectedRevision = &one
	edit.RequestID = uuid.NewString()
	if _, err := f.store.SetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging", edit); err != nil {
		t.Fatal(err)
	}
	out = callTool(t, ctx, cs, "gregale_get_environment_cache_schedule", args, false)
	if string(out["convergence_status"]) != `"policy_superseded"` {
		t.Fatal("superseded scheduled policy reported converged")
	}
}
