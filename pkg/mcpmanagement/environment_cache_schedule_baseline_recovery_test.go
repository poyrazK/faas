package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const recoverActivityTool = "gregale_recover_environment_cache_schedule_activity"

func recoveryArgs(token string, limit int) map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "recovery_token": token, "limit": limit}
}
func decodeBaseline(t *testing.T, out map[string]json.RawMessage) environmentScheduleRecoveryResult {
	t.Helper()
	raw, _ := json.Marshal(out)
	var result environmentScheduleRecoveryResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCacheScheduleBaselineRecoveryReconnectAndCatchUp(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	opts.AllowWrites = false
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	first := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), false))
	if first.Complete || len(first.Entries) != 1 || first.RecoveryToken == "" || first.ActivityCursor != "" {
		t.Fatal(first)
	}
	token, err := decodeScheduleRecoveryToken(first.RecoveryToken)
	if err != nil {
		t.Fatal(err)
	}
	c, err := api.ParseEnvironmentCacheActivityCursor(token.Checkpoint, f.account.ID, f.app.ID, f.project.ID, f.env.ID)
	if err != nil || c.Sequence != 2 {
		t.Fatal("checkpoint did not capture actual head", c, err)
	}
	cs.Close()
	// This sorts before the already returned baseline page. Catch-up must recover
	// it even though the live discovery continuation cannot see it.
	created := seedSchedule(t, f, time.Now().Add(30*time.Minute))
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	last := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs(first.RecoveryToken, 2), false))
	if !last.Complete || last.RecoveryToken != "" || len(last.Entries) != 1 || last.ActivityCursor != token.Checkpoint {
		t.Fatal(last)
	}
	page := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(last.ActivityCursor, 10), false))
	if len(page.Entries) != 1 || page.Entries[0].ScheduleID != created.ScheduleID || page.Entries[0].Sequence != 3 {
		t.Fatal("baseline lost concurrent earlier-sorting schedule", page)
	}
	if f.writes != 0 {
		t.Fatal("recovery performed writes")
	}
}

func TestCacheScheduleBaselineRecoveryEmptyAndConcurrentCreation(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "arrival"}[create], func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			listed := false
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if create && !listed && r.URL.Path == "/v1/apps/alpha/cache/schedules" {
					listed = true
					seedScheduleContext(r.Context(), t, f, time.Now().Add(time.Hour))
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer b.Close()
			opts.APIBase = b.URL
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			result := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 10), false))
			if !result.Complete || result.ActivityCursor == "" || result.RecoveryToken != "" || result.Entries == nil {
				t.Fatal(result)
			}
			c, err := api.ParseEnvironmentCacheActivityCursor(result.ActivityCursor, f.account.ID, f.app.ID, f.project.ID, f.env.ID)
			if err != nil || c.Sequence != 0 {
				t.Fatal("checkpoint moved after discovery", c, err)
			}
			page := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs(result.ActivityCursor, 10), false))
			if create && (len(result.Entries) != 1 || len(page.Entries) != 1) || !create && (len(result.Entries) != 0 || len(page.Entries) != 0) {
				t.Fatal(result, page)
			}
		})
	}
}

func TestCacheScheduleBaselineRecoveryRejectsInvalidTokens(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	first := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), false))
	original, e := decodeScheduleRecoveryToken(first.RecoveryToken)
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"endpoint", "account", "app", "project", "environment", "scope", "checkpoint", "after", "version"} {
		token := original
		switch field {
		case "endpoint":
			token.Endpoint = "https://other.example"
		case "account":
			token.Account = "other"
		case "app":
			token.App = "other"
		case "project":
			token.Project = "other"
		case "environment":
			token.Environment = "other"
		case "scope":
			token.Scope = "production"
		case "checkpoint":
			token.Checkpoint = "forged"
		case "after":
			token.After = "forged"
		case "version":
			token.Version = 2
		}
		raw, _ := json.Marshal(token)
		callTool(t, ctx, cs, recoverActivityTool, recoveryArgs(base64.RawURLEncoding.EncodeToString(raw), 1), true)
	}
	for _, token := range []string{"forged", strings.Repeat("x", api.EnvironmentCacheActivityRecoveryTokenMaxBytes+1), base64.RawURLEncoding.EncodeToString([]byte(`{} {}`))} {
		callTool(t, ctx, cs, recoverActivityTool, recoveryArgs(token, 1), true)
	}
	for _, limit := range []int{-1, 51} {
		callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", limit), true)
	}
	cs.Close()
	delete(opts.Environments.Named, "shop/staging")
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	callTool(t, ctx, cs, recoverActivityTool, recoveryArgs(first.RecoveryToken, 1), true)
}

func TestCacheScheduleBaselineRecoveryRestartsWhenOvertaken(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	first := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), false))
	seedSchedule(t, f, time.Now().Add(3*time.Hour))
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, ctx, cs, recoverActivityTool, recoveryArgs(first.RecoveryToken, 1), true)
	if string(out["code"]) != `"recovery_restart_required"` || string(out["status"]) != "410" || out["activity_recovery"] == nil || out["entries"] != nil {
		t.Fatal(out)
	}
	fresh := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), false))
	token, err := decodeScheduleRecoveryToken(fresh.RecoveryToken)
	if err != nil || token.Checkpoint == "" || fresh.Entries[0].ScheduleID != first.Entries[0].ScheduleID {
		t.Fatal("restart did not begin a fresh baseline", fresh, err)
	}
	c, err := api.ParseEnvironmentCacheActivityCursor(token.Checkpoint, f.account.ID, f.app.ID, f.project.ID, f.env.ID)
	if err != nil || c.Sequence != 3 {
		t.Fatal(c, err)
	}
}

func TestCacheScheduleBaselineRecoveryWithholdsOvertakenOrRevokedPage(t *testing.T) {
	for _, mode := range []string{"retention", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			seedSchedule(t, f, time.Now().Add(time.Hour))
			revoked := false
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if revoked {
					http.Error(w, "revoked", 403)
					return
				}
				if r.URL.Path == "/v1/apps/alpha/cache/schedules" {
					page := httptest.NewRecorder()
					base.Config.Handler.ServeHTTP(page, r)
					if mode == "retention" {
						seedScheduleContext(r.Context(), t, f, time.Now().Add(2*time.Hour))
						if err := f.store.PruneEnvironmentCacheScheduleActivity(r.Context(), time.Now().Add(time.Minute)); err != nil {
							t.Error(err)
						}
					} else {
						revoked = true
					}
					for key, values := range page.Header() {
						for _, value := range values {
							w.Header().Add(key, value)
						}
					}
					w.WriteHeader(page.Code)
					_, _ = w.Write(page.Body.Bytes())
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer b.Close()
			opts.APIBase = b.URL
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			out := callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 10), true)
			if out["entries"] != nil || out["recovery_token"] != nil || out["activity_cursor"] != nil {
				t.Fatal("unsafe page leaked", out)
			}
			if mode == "retention" && string(out["code"]) != `"recovery_restart_required"` {
				t.Fatal(out)
			}
		})
	}
}

func TestCacheScheduleBaselineRecoveryRejectsForgedCheckpoint(t *testing.T) {
	for _, mode := range []string{"scope", "account", "bounds", "empty", "retention"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/alpha/cache/schedules/activity/checkpoint" {
					base.Config.Handler.ServeHTTP(w, r)
					return
				}
				point, err := f.store.GetEnvironmentCacheScheduleActivityCheckpoint(r.Context(), f.account.ID, f.app.ID, "staging")
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				switch mode {
				case "scope":
					point.EnvironmentID = "other"
				case "account":
					point.RestartCursor = api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: "other", AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID})
				case "bounds":
					point.OldestCursor = api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: f.account.ID, AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, Sequence: 1})
				case "empty":
					point.RestartCursor = ""
				case "retention":
					point.RetentionSeconds = 0
				}
				_ = json.NewEncoder(w).Encode(point)
			}))
			defer b.Close()
			opts.APIBase = b.URL
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			out := callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), true)
			if string(out["code"]) != `"invalid_response"` {
				t.Fatal(out)
			}
		})
	}
}

func TestCacheScheduleBaselineRecoveryCancellationRetriesSameToken(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	var block atomic.Bool
	entered, released := make(chan struct{}), make(chan struct{})
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha/cache/schedules" && block.Swap(false) {
			close(entered)
			<-r.Context().Done()
			close(released)
			return
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	defer b.Close()
	opts.APIBase = b.URL
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	first := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), false))
	block.Store(true)
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(callCtx, &mcp.CallToolParams{Name: recoverActivityTool, Arguments: recoveryArgs(first.RecoveryToken, 1)})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("baseline read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not cancel")
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("native read did not cancel")
	}
	cs.Close()
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	last := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs(first.RecoveryToken, 1), false))
	if !last.Complete || len(last.Entries) != 1 || last.Entries[0].ScheduleID == first.Entries[0].ScheduleID {
		t.Fatal("cancellation consumed recovery progress", last)
	}
}
