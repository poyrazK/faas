package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestScheduleCompatibilityPreflightMixedVersions(t *testing.T) {
	for _, tc := range []struct {
		name, want, key              string
		caps                         *api.EnvironmentCacheScheduleCapabilities
		capsStatus, checkpointStatus int
		foreign                      bool
	}{
		{name: "legacy omission", want: "unknown", key: "schedule_activity"},
		{name: "legacy endpoint", capsStatus: 404, want: "unknown", key: "schedule_checkpoint"},
		{name: "capabilities permission", capsStatus: 403, want: "blocked", key: "schedule_activity"},
		{name: "unsupported checkpoint", caps: &api.EnvironmentCacheScheduleCapabilities{Listing: true, Activity: true, RetentionSeconds: api.EnvironmentCacheActivityRetentionSeconds}, want: "unsupported", key: "schedule_checkpoint"},
		{name: "unsupported retention", caps: &api.EnvironmentCacheScheduleCapabilities{Listing: true, Activity: true, Checkpoint: true}, want: "unsupported", key: "schedule_retention"},
		{name: "current", caps: currentScheduleCapabilities(), want: "passed", key: "schedule_activity"},
		{name: "checkpoint denied", caps: currentScheduleCapabilities(), checkpointStatus: 403, want: "blocked", key: "schedule_checkpoint"},
		{name: "mixed replica", caps: currentScheduleCapabilities(), checkpointStatus: 404, want: "unknown", key: "schedule_checkpoint"},
		{name: "foreign checkpoint", caps: currentScheduleCapabilities(), foreign: true, want: "unknown", key: "schedule_checkpoint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			var scheduleReads atomic.Int64
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("preflight wrote %s", r.Method)
				}
				if r.URL.Path == "/v1/capabilities" {
					if tc.capsStatus != 0 {
						api.WriteProblem(w, api.NewProblem(tc.capsStatus, api.CodeForbidden, "Private body", "must not be echoed"))
						return
					}
					_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{EnvironmentCacheSchedules: tc.caps})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/cache/schedules") {
					scheduleReads.Add(1)
					if !strings.HasSuffix(r.URL.Path, "/checkpoint") && r.URL.Query().Get("limit") != "1" {
						t.Errorf("unbounded preflight read: %s", r.URL)
					}
					if strings.HasSuffix(r.URL.Path, "/checkpoint") {
						if tc.checkpointStatus != 0 {
							api.WriteProblem(w, api.NewProblem(tc.checkpointStatus, api.CodeForbidden, "Private body", "must not be echoed"))
							return
						}
						if tc.foreign {
							_ = json.NewEncoder(w).Encode(api.EnvironmentCacheActivityRecovery{AppID: "foreign"})
							return
						}
					}
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer b.Close()
			opts.APIBase, opts.AllowWrites = b.URL, false
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			out := callTool(t, ctx, cs, "gregale_preflight", map[string]any{"app": "alpha", "project": "shop", "environment": "staging"}, false)
			var checks map[string]preflightCheck
			if err := json.Unmarshal(out["checks"], &checks); err != nil {
				t.Fatal(err)
			}
			if checks[tc.key].Status != tc.want || checks["schedule_recovery"].Status != tc.want {
				t.Fatalf("unexpected result: %+v", checks)
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "must not be echoed") || !strings.Contains(string(out["schedule_activity_guidance"]), "Upgrade") {
				t.Fatal("unsafe or missing guidance")
			}
			if tc.caps == nil && scheduleReads.Load() != 0 {
				t.Fatal("probed unavailable workflow")
			}
			if f.writes != 0 {
				t.Fatal("preflight mutated intent")
			}
		})
	}
}

func currentScheduleCapabilities() *api.EnvironmentCacheScheduleCapabilities {
	return &api.EnvironmentCacheScheduleCapabilities{Listing: true, Activity: true, Checkpoint: true, RetentionSeconds: api.EnvironmentCacheActivityRetentionSeconds}
}

func TestScheduleRecoveryFailsClosedAcrossBackendVersions(t *testing.T) {
	for _, tc := range []struct {
		name, want                   string
		caps                         *api.EnvironmentCacheScheduleCapabilities
		capsStatus, checkpointStatus int
	}{
		{name: "omitted", want: "backend_upgrade_required"},
		{name: "unsupported", caps: &api.EnvironmentCacheScheduleCapabilities{}, want: "backend_upgrade_required"},
		{name: "old capability endpoint", capsStatus: 404, want: "backend_capabilities_unavailable"},
		{name: "denied capabilities", capsStatus: 403, want: api.CodeForbidden},
		{name: "advertised then missing checkpoint", caps: currentScheduleCapabilities(), checkpointStatus: 404, want: api.CodeNotFound},
		{name: "advertised then denied checkpoint", caps: currentScheduleCapabilities(), checkpointStatus: 403, want: api.CodeForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			var fallbackReads atomic.Int64
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/capabilities" {
					if tc.capsStatus != 0 {
						api.WriteProblem(w, api.NewProblem(tc.capsStatus, api.CodeForbidden, "Denied", "Denied"))
						return
					}
					_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{EnvironmentCacheSchedules: tc.caps})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/activity/checkpoint") && tc.checkpointStatus != 0 {
					code := api.CodeNotFound
					if tc.checkpointStatus == 403 {
						code = api.CodeForbidden
					}
					api.WriteProblem(w, api.NewProblem(tc.checkpointStatus, code, "Unavailable", "Unavailable"))
					return
				}
				if strings.HasSuffix(r.URL.Path, "/activity") || r.URL.Path == "/v1/apps/alpha/cache/schedules" {
					fallbackReads.Add(1)
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer b.Close()
			opts.APIBase = b.URL
			cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			out := callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), true)
			if string(out["code"]) != `"`+tc.want+`"` || fallbackReads.Load() != 0 || f.writes != 0 {
				t.Fatalf("unsafe recovery: %+v reads=%d", out, fallbackReads.Load())
			}
			if out["activity_cursor"] != nil || out["entries"] != nil || out["recovery_token"] != nil {
				t.Fatal("failed recovery returned progress")
			}
			if tc.checkpointStatus == 404 && (!strings.Contains(string(out["hint"]), "Verify the target") || !strings.Contains(string(out["hint"]), "Upgrade")) {
				t.Fatal("missing endpoint lost compatibility guidance")
			}
		})
	}
}

func TestScheduleRecoveryRechecksCapabilitiesOnContinuation(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	var withdrawn atomic.Bool
	var discoveryReads atomic.Int64
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if withdrawn.Load() && r.URL.Path == "/v1/capabilities" {
			_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{})
			return
		}
		if r.URL.Path == "/v1/apps/alpha/cache/schedules" {
			discoveryReads.Add(1)
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	defer b.Close()
	opts.APIBase = b.URL
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	first := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), false))
	withdrawn.Store(true)
	out := callTool(t, ctx, cs, recoverActivityTool, recoveryArgs(first.RecoveryToken, 1), true)
	if first.RecoveryToken == "" || string(out["code"]) != `"backend_upgrade_required"` || discoveryReads.Load() != 1 || out["activity_cursor"] != nil {
		t.Fatalf("continued unsafe discovery: %+v", out)
	}
}
