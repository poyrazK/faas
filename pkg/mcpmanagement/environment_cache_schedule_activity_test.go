package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func activityArgs(after string, limit int) map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "after": after, "limit": limit}
}
func decodeActivity(t *testing.T, out map[string]json.RawMessage) api.EnvironmentCacheScheduleActivity {
	t.Helper()
	raw, e := json.Marshal(out)
	if e != nil {
		t.Fatal(e)
	}
	var page api.EnvironmentCacheScheduleActivity
	if e = json.Unmarshal(raw, &page); e != nil {
		t.Fatal(e)
	}
	return page
}
func TestCacheScheduleActivityMCPCatchUpAndReadConsent(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	opts.AllowWrites = false
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	empty := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs("", 2), false))
	if empty.Cursor == "" || empty.HasMore || len(empty.Entries) != 0 {
		t.Fatal("no empty checkpoint", empty)
	}
	a := seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(time.Hour))
	if _, e := f.store.CancelEnvironmentCacheSchedule(ctx, f.account.ID, f.app.ID, "staging", a.ScheduleID); e != nil {
		t.Fatal(e)
	}
	first := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(empty.Cursor, 2), false))
	if len(first.Entries) != 2 || !first.HasMore || first.Entries[0].Kind != "accepted" || first.Entries[1].Sequence != 2 {
		t.Fatal("first page", first)
	}
	next := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(first.Cursor, 2), false))
	if len(next.Entries) != 1 || next.Entries[0].Kind != "cancelled" || next.Entries[0].Sequence != 3 || next.HasMore {
		t.Fatal("missed cancellation", next)
	}
	_ = cs.Close()
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	caught := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(next.Cursor, 2), false))
	if caught.Cursor != next.Cursor || len(caught.Entries) != 0 {
		t.Fatal("checkpoint did not survive reconnect", caught)
	}
	seedSchedule(t, f, time.Now().Add(time.Hour))
	resumed := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(caught.Cursor, 2), false))
	if len(resumed.Entries) != 1 || resumed.Entries[0].Sequence != 4 {
		t.Fatal("reconnect skipped new acceptance", resumed)
	}
	bad := activityArgs(first.Cursor, 2)
	bad["environment"] = "production"
	callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", bad, true)
	cursor, e := api.ParseEnvironmentCacheActivityCursor(first.Cursor, f.account.ID, f.app.ID, f.project.ID, f.env.ID)
	if e != nil {
		t.Fatal(e)
	}
	cursor.AccountID = uuid.NewString()
	callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(api.EncodeEnvironmentCacheActivityCursor(cursor), 2), true)
	cursor.AccountID = f.account.ID
	cursor.Sequence = 100
	callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(api.EncodeEnvironmentCacheActivityCursor(cursor), 2), true)
	if f.writes != 0 {
		t.Fatal("read-only feed mutated schedule state")
	}
	_ = cs.Close()
	delete(opts.Environments.Named, "shop/staging")
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(resumed.Cursor, 2), true)
}
func TestCacheScheduleActivityMCPRejectsForgedEvidence(t *testing.T) {
	for _, mode := range []string{"scope", "gap", "cursor", "outcome", "convergence"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			seedSchedule(t, f, time.Now().Add(time.Hour))
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/alpha/cache/schedules/activity" {
					base.Config.Handler.ServeHTTP(w, r)
					return
				}
				page, e := f.store.GetEnvironmentCacheScheduleActivity(r.Context(), f.account.ID, f.app.ID, "staging", "", 2)
				if e != nil {
					t.Error(e)
					w.WriteHeader(500)
					return
				}
				switch mode {
				case "scope":
					page.EnvironmentID = uuid.NewString()
				case "gap":
					page.Entries[0].Sequence = 2
				case "cursor":
					c, _ := api.ParseEnvironmentCacheActivityCursor(page.Cursor, f.account.ID, f.app.ID, f.project.ID, f.env.ID)
					c.Sequence = 0
					page.Cursor = api.EncodeEnvironmentCacheActivityCursor(c)
				case "outcome":
					page.Entries[0].Kind = "executed"
					page.Entries[0].Status = "executed"
				case "convergence":
					page.Entries[0].Kind = "converged"
					page.Entries[0].Status = "converged"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(page)
			}))
			t.Cleanup(backend.Close)
			opts.APIBase = backend.URL
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs("", 2), true)
			if string(out["code"]) != `"invalid_response"` {
				t.Fatal("forged activity not rejected", out)
			}
		})
	}
}
