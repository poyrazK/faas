package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCacheScheduleActivityMCPExpiredRecovery(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts.AllowWrites = false
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	initial := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs("", 1), false)).Cursor
	seedScheduleContext(r.Context(), t, f, time.Now().Add(time.Hour))
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"gregale_get_environment_cache_schedule_activity", "gregale_watch_environment_cache_schedule_activity"} {
		out := callTool(t, ctx, cs, tool, activityArgs(initial, 1), true)
		var recovery api.EnvironmentCacheActivityRecovery
		if string(out["code"]) != `"cursor_expired"` || json.Unmarshal(out["activity_recovery"], &recovery) != nil || recovery.EnvironmentID != f.env.ID || recovery.RestartCursor == "" {
			t.Fatal(out)
		}
		seedScheduleContext(r.Context(), t, f, time.Now().Add(time.Hour))
		page := decodeActivity(t, callTool(t, ctx, cs, tool, activityArgs(recovery.RestartCursor, 1), false))
		if len(page.Entries) != 1 {
			t.Fatal("recovery missed concurrent event", page)
		}
	}
	if f.writes != 0 {
		t.Fatal("expiry recovery wrote state")
	}
}

func TestCacheScheduleActivityMCPRejectsForgedRecovery(t *testing.T) {
	for _, mode := range []string{"scope", "account", "floor", "head", "empty", "retention", "status", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			delivered := false
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if delivered && mode == "revoked" {
					http.Error(w, "revoked", 403)
					return
				}
				if r.URL.Path != "/v1/apps/alpha/cache/schedules/activity" {
					base.Config.Handler.ServeHTTP(w, r)
					return
				}
				delivered = true
				floor := api.EnvironmentCacheActivityCursor{AccountID: f.account.ID, AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, Sequence: 1}
				head := floor
				head.Sequence = 2
				recovery := api.EnvironmentCacheActivityRecovery{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, Scope: "staging", RetentionSeconds: api.EnvironmentCacheActivityRetentionSeconds}
				switch mode {
				case "scope":
					recovery.EnvironmentID = "other"
				case "account":
					floor.AccountID = "other"
				case "floor":
					floor.Sequence = 0
				case "head":
					head.Sequence = 0
				case "retention":
					recovery.RetentionSeconds = 0
				}
				recovery.OldestCursor = api.EncodeEnvironmentCacheActivityCursor(floor)
				recovery.RestartCursor = api.EncodeEnvironmentCacheActivityCursor(head)
				if mode == "empty" {
					recovery.RestartCursor = ""
				}
				status := 410
				if mode == "status" {
					status = 409
				}
				p := api.NewProblem(status, api.CodeActivityCursorExpired, "Expired", "Expired")
				p.ActivityRecovery = &recovery
				api.WriteProblem(w, p)
			}))
			defer backend.Close()
			opts.APIBase = backend.URL
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs("", 1), true)
			if _, ok := out["activity_recovery"]; ok {
				t.Fatal("unvalidated recovery leaked", out)
			}
			if mode != "revoked" && string(out["code"]) != `"invalid_response"` {
				t.Fatal(out)
			}
		})
	}
}

func TestCacheScheduleActivityMCPWatchExpiresDuringWait(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	reads := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha/cache/schedules/activity" {
			reads++
			if reads == 2 {
				seedScheduleContext(r.Context(), t, f, time.Now().Add(time.Hour))
				if err := f.store.PruneEnvironmentCacheScheduleActivity(r.Context(), time.Now().Add(time.Minute)); err != nil {
					t.Error(err)
				}
			}
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	defer backend.Close()
	opts.APIBase = backend.URL
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	args := activityArgs("", 1)
	args["wait_seconds"] = 3
	out := callTool(t, ctx, cs, "gregale_watch_environment_cache_schedule_activity", args, true)
	if string(out["code"]) != `"cursor_expired"` || reads != 2 {
		t.Fatal("watch silently skipped cleanup gap", out, reads)
	}
}
