package mcpmanagement

import (
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCacheScheduleActivityWatchTimeoutAndReconnect(t *testing.T) {
	f, b, opts := newEnvironmentScheduleFixture(t)
	opts.AllowWrites = false
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	checkpoint := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs("", 1), false)).Cursor
	args := activityArgs(checkpoint, 1)
	args["wait_seconds"] = 1
	out := callTool(t, ctx, cs, "gregale_watch_environment_cache_schedule_activity", args, false)
	if string(out["watch_status"]) != `"timed_out"` || string(out["watch_timed_out"]) != "true" || decodeActivity(t, out).Cursor != checkpoint {
		t.Fatal(out)
	}
	cs.Close()
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(time.Hour))
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	out = callTool(t, ctx, cs, "gregale_watch_environment_cache_schedule_activity", args, false)
	page := decodeActivity(t, out)
	if string(out["watch_status"]) != `"events"` || len(page.Entries) != 1 || !page.HasMore || page.Entries[0].Sequence != 1 {
		t.Fatal(out)
	}
	args["wait_seconds"] = 46
	callTool(t, ctx, cs, "gregale_watch_environment_cache_schedule_activity", args, true)
	if f.writes != 0 {
		t.Fatal("watch performed writes")
	}
}

func TestCacheScheduleActivityWatchPollsAndRechecksAccess(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "arrival", true: "revoked"}[denied], func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			reads := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha/cache/schedules/activity" {
					reads++
					if reads == 2 {
						if denied {
							http.Error(w, "consent revoked", http.StatusForbidden)
							return
						}
						seedSchedule(t, f, time.Now().Add(time.Hour))
					}
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer backend.Close()
			opts.APIBase = backend.URL
			opts.AllowWrites = false
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			args := activityArgs("", 1)
			args["wait_seconds"] = 3
			out := callTool(t, ctx, cs, "gregale_watch_environment_cache_schedule_activity", args, denied)
			if !denied && (len(decodeActivity(t, out).Entries) != 1 || string(out["watch_status"]) != `"events"`) {
				t.Fatal(out)
			}
			if reads != 2 {
				t.Fatalf("reads=%d", reads)
			}
		})
	}
}

func TestCacheScheduleActivityWatchCancellationKeepsCheckpoint(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	entered, released := make(chan struct{}), make(chan struct{})
	reads := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha/cache/schedules/activity" {
			reads++
			if reads == 2 {
				close(entered)
				<-r.Context().Done()
				close(released)
				return
			}
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	defer backend.Close()
	opts.APIBase = backend.URL
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	checkpoint := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs("", 1), false)).Cursor
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	args := activityArgs(checkpoint, 1)
	args["wait_seconds"] = 5
	go func() {
		_, err := cs.CallTool(callCtx, &mcp.CallToolParams{Name: "gregale_watch_environment_cache_schedule_activity", Arguments: args})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation blocked")
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("native read not cancelled")
	}
	seedSchedule(t, f, time.Now().Add(time.Hour))
	out := callTool(t, ctx, cs, "gregale_watch_environment_cache_schedule_activity", args, false)
	if page := decodeActivity(t, out); len(page.Entries) != 1 || page.Entries[0].Sequence != 1 {
		t.Fatal(out)
	}
}

func TestCacheScheduleActivityWatchEarlierDeadlineIsError(t *testing.T) {
	_, backend, opts := newEnvironmentScheduleFixture(t)
	opts.Timeout = 200 * time.Millisecond
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	args := activityArgs("", 1)
	args["wait_seconds"] = 3
	out := callTool(t, ctx, cs, "gregale_watch_environment_cache_schedule_activity", args, true)
	if _, ok := out["watch_status"]; ok {
		t.Fatal("request deadline became successful watch timeout", out)
	}
}
