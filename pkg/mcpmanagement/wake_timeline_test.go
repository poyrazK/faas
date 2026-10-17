// adr: 1635
package mcpmanagement

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const wakeTimelineSince = "2026-10-16T10:00:00Z"
const wakeTimelineCursor = "2026-10-16T10:59:00.112Z"

func wakeTimelineFixture(appID string, limit int, empty bool) api.WakeTimelineResponse {
	data := func() map[string]any {
		return map[string]any{"app_id": appID, "wake_id": "wake-1", "request_id": "excluded-request", "node_id": "excluded-node", "instance_id": "excluded-instance", "reason": "excluded-error " + testToken, "future": map[string]any{"secret": "excluded-private"}}
	}
	rows := []api.WakeTimelineEvent{
		{At: "2026-10-16T10:59:00Z", Kind: "wake.queue_accepted", Actor: "excluded-actor", Data: data()},
		{At: "2026-10-16T10:59:00.001Z", Kind: "wake.admitted", Data: data()},
		{At: "2026-10-16T10:59:00.002Z", Kind: "wake.boot_started", Data: data()},
		{At: "2026-10-16T10:59:00.102Z", Kind: "wake.boot_completed", Data: data()},
		{At: "2026-10-16T10:59:00.103Z", Kind: "wake.readiness_200", Data: data()},
		{At: wakeTimelineCursor, Kind: "wake.proxy_first_byte", Data: data()},
		{At: wakeTimelineCursor, Kind: "wake.restore_breakdown", Data: data()},
		{At: wakeTimelineCursor, Kind: "excluded-future-kind " + testToken, Data: data()},
	}
	rows[0].Data["queue_wait_ms"] = "excluded-not-a-measurement"
	rows[2].Data["method"], rows[2].Data["tier"], rows[2].Data["trigger"] = "restore", "warm", "gateway "+testToken
	rows[2].Data["queued_count"], rows[2].Data["concurrency_at_admit"], rows[2].Data["at_capacity"] = float64(0), float64(0), false
	rows[2].Data["requested_at"] = "2026-10-16T10:59:00Z"
	rows[3].Data["method"], rows[3].Data["tier"], rows[3].Data["restore_fallback_reason"] = "cold_boot", "cold_boot_fallback", "after_restore_failed"
	rows[3].Data["started_at"], rows[3].Data["completed_at"] = "2026-10-16T10:59:00.002Z", "2026-10-16T10:59:00.102Z"
	rows[4].Data["elapsed_ms"], rows[4].Data["probe_count"], rows[4].Data["healthcheck_path"] = float64(0), float64(1), "excluded-private-path"
	rows[5].Data["latency_ms"], rows[5].Data["proxy_latency_ms"] = float64(112), float64(10)
	rows[5].Data["gateway_phases_ms"] = map[string]any{"excluded-phase": 1}
	rows[6].Data["total_ms"], rows[6].Data["wait_ready_ms"] = float64(90), float64(5)
	rows[6].Data["resolve_artifacts"] = []any{"excluded-artifacts"}
	n := api.WakeTimelineResponse{AppID: appID, WakeID: "wake-1", Limit: limit, NextCursor: wakeTimelineCursor, Events: rows}
	if empty {
		n.Events, n.NextCursor = nil, ""
	}
	return n
}

func TestWakeTimelineConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "allowlist_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("timeline wrote native state")
						w.WriteHeader(405)
						return
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/apps/alpha/wakes/wake-1/timeline":
						reads.Add(1)
						q := r.URL.Query()
						if q.Get("since") != wakeTimelineSince || q.Get("limit") != "8" || q.Get("pagination") != "keyset_v1" || len(q) != 3 {
							t.Error("native exclusive selection changed or unsupported argument forwarded")
						}
						_ = json.NewEncoder(w).Encode(wakeTimelineFixture("app-1", 8, false))
					default:
						t.Errorf("unexpected enrichment %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL}
				if mode != "local" && mode != "allowlist_denied" {
					opts.Environments = &EnvironmentConsent{RequestReadApps: map[string]string{}}
					if mode == "approved" {
						opts.Environments.RequestReadApps["alpha"] = "app-1"
					}
					if mode == "replaced" {
						opts.Environments.RequestReadApps["alpha"] = "old-app"
					}
				}
				if mode == "allowlist_denied" {
					opts.AllowedApps = map[string]string{"other": "other-app"}
				}
				denied := mode == "denied" || mode == "replaced" || mode == "allowlist_denied"
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				args := wakeTimelineArgs{App: "alpha", WakeID: "wake-1", Since: wakeTimelineSince, Limit: 8}
				out := callTool(t, ctx, cs, "gregale_get_wake_timeline", args, denied)
				if denied {
					if reads.Load() != 0 || mode != "allowlist_denied" && string(out["code"]) != `"request_access_denied"` {
						t.Fatal("wake timeline bypassed consent")
					}
					return
				}
				encoded, _ := json.Marshal(out)
				var v wakeTimelineView
				_ = json.Unmarshal(encoded, &v)
				if reads.Load() != 1 || v.AppID != "app-1" || v.WakeID != "wake-1" || v.Scope != "app_wide" || v.RequestedSince != args.Since || v.LowerBound != "exclusive" || v.NativeCount != 8 || v.ReturnedCount != 8 || v.PaginationStatus != "timestamp_only_not_lossless" || !v.NativeMoreEventsHint || v.NativeCursorAt != wakeTimelineCursor || v.SourceCompleteness != "unknown" || v.Freshness != "unknown" || v.UnknownEventCount != 1 {
					t.Fatal("lost timeline identity, limits or uncertainty")
				}
				boot, complete, ready, proxy := v.Events[2].Details, v.Events[3].Details, v.Events[4].Details, v.Events[5].Details
				if boot.QueuedCount == nil || *boot.QueuedCount != 0 || boot.ConcurrencyAtAdmit == nil || *boot.ConcurrencyAtAdmit != 0 || boot.AtCapacity == nil || *boot.AtCapacity || !boot.LabelsModified || boot.AdmissionCountBasis != "native_admission_concurrency_ledger" || complete.RestoreFallbackReason != "after_restore_failed" || complete.RestoreFallbackStatus != "observed" || ready.ReadinessElapsedMS == nil || *ready.ReadinessElapsedMS != 0 || proxy.AcceptanceToFirstByteMS == nil || *proxy.AcceptanceToFirstByteMS != 112 || proxy.PostWakeProxyMS == nil || *proxy.PostWakeProxyMS != 10 {
					t.Fatal("lost raw payload zero/false presence, fallback or timing boundaries")
				}
				if v.Events[6].Details.NativeCallBasis != "restore_call" || v.Events[6].Details.NativeCallTotalMS == nil || *v.Events[6].Details.NativeCallTotalMS != 90 || v.Events[7].Kind != "unrecognized_event" || v.Events[7].Details.Status != "not_exposed_for_kind" || v.StageObservations[5].Status != "not_returned" || v.StageObservations[2].EventCount != 3 || out["next_arguments"] != nil {
					t.Fatal("unknown stages or timestamp cursor became a complete narrative/continuation")
				}
				for _, marker := range []string{"excluded-", testToken, `"data"`, `"actor"`, `"node_id"`, `"request_id"`, `"queue_wait_ms"`, `"gateway_phases_ms"`} {
					if strings.Contains(string(encoded), marker) {
						t.Errorf("timeline leaked %s", marker)
					}
				}
				if mode == "approved" {
					args.App = "future"
					callTool(t, ctx, cs, "gregale_get_wake_timeline", args, true)
					if reads.Load() != 1 {
						t.Fatal("diagnostic grant widened to future apps")
					}
				}
			})
		}
	}
}

func TestWakeTimelineSelections(t *testing.T) {
	for _, tc := range []struct {
		name string
		args wakeTimelineArgs
		bad  bool
	}{
		{name: "default"}, {name: "max", args: wakeTimelineArgs{Limit: 100}},
		{name: "microseconds", args: wakeTimelineArgs{Since: "2026-10-16T12:00:00.123456789+02:00"}},
		{name: "future_filter", args: wakeTimelineArgs{Since: "2099-01-01T00:00:00Z"}},
		{name: "relative", args: wakeTimelineArgs{Since: "1h"}, bad: true},
		{name: "bad_time", args: wakeTimelineArgs{Since: "bad"}, bad: true},
		{name: "zero_time", args: wakeTimelineArgs{Since: "0001-01-01T00:00:00Z"}, bad: true},
		{name: "collapsed_zero", args: wakeTimelineArgs{Since: "0001-01-01T00:00:00.000000001Z"}, bad: true},
		{name: "controls", args: wakeTimelineArgs{Since: wakeTimelineSince + "\n"}, bad: true},
		{name: "long_time", args: wakeTimelineArgs{Since: strings.Repeat("x", 65)}, bad: true},
		{name: "negative_limit", args: wakeTimelineArgs{Limit: -1}, bad: true},
		{name: "large_limit", args: wakeTimelineArgs{Limit: 101}, bad: true},
		{name: "path_id", args: wakeTimelineArgs{WakeID: "../other"}, bad: true},
		{name: "empty_id", args: wakeTimelineArgs{WakeID: " "}, bad: true},
		{name: "secret_id", args: wakeTimelineArgs{WakeID: testToken}, bad: true},
		{name: "long_id", args: wakeTimelineArgs{WakeID: strings.Repeat("x", 129)}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.args.App = "alpha"
			if tc.args.WakeID == "" {
				tc.args.WakeID = "wake-1"
			}
			args, err := (&management{opts: Options{Token: testToken}}).wakeTimelineSelection(tc.args)
			if (err != nil) != tc.bad {
				t.Fatalf("selection=%+v error=%v", args, err)
			}
			if tc.bad {
				return
			}
			if tc.args.Limit == 0 && args.Limit != 50 || tc.name == "default" && args.Since != "" || tc.name == "microseconds" && args.Since != "2026-10-16T10:00:00.123456Z" {
				t.Fatal("selection changed native defaults or source precision")
			}
		})
	}
}

func TestWakeTimelineNativeValidation(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "no_hint", "legacy_missing", "null", "unknown_fallback", "unknown_payload", "cursor_before_visible_tail", "repeated_kind", "zero_producer_time", "boot_observed", "boot_failed", "cold_boot_breakdown", "park_started", "park_completed", "park_failed", "wrong_app", "wrong_wake", "wrong_limit", "over_limit", "missing_app_id", "cross_app_row", "cross_wake_row", "missing_data", "bad_at", "unsorted", "at_since", "bad_hint", "hint_at_since", "empty_hint", "negative", "fractional", "overflow", "nan", "inf", "string_number", "wrong_bool", "wrong_label", "wrong_producer_time", "wrong_time_type"} {
		t.Run(mode, func(t *testing.T) {
			n := wakeTimelineFixture("app-1", 8, mode == "empty" || mode == "empty_hint")
			args := wakeTimelineArgs{App: "alpha", WakeID: "wake-1", Since: wakeTimelineSince, Limit: 8}
			good := true
			switch mode {
			case "no_hint":
				n.NextCursor = ""
			case "legacy_missing":
				delete(n.Events[2].Data, "queued_count")
				delete(n.Events[2].Data, "concurrency_at_admit")
				delete(n.Events[2].Data, "at_capacity")
				delete(n.Events[4].Data, "elapsed_ms")
			case "null":
				n.Events[2].Data["queued_count"], n.Events[2].Data["at_capacity"], n.Events[4].Data["elapsed_ms"] = nil, nil, nil
			case "unknown_fallback":
				n.Events[3].Data["restore_fallback_reason"] = "excluded-unknown " + testToken
			case "unknown_payload":
				n.Events[7].Data["elapsed_ms"] = "excluded-bad-future"
			case "cursor_before_visible_tail":
				n.NextCursor = n.Events[3].At
			case "repeated_kind":
				n.Events[7].Kind = "wake.proxy_first_byte"
			case "zero_producer_time":
				n.Events[3].Data["started_at"] = "0001-01-01T00:00:00Z"
			case "boot_observed", "boot_failed", "cold_boot_breakdown", "park_started", "park_completed", "park_failed":
				n.Events[7].Kind = "wake." + mode
				n.Events[7].Data["method"] = "restore"
				for _, key := range []string{"observed_at", "started_at", "completed_at", "failed_at"} {
					n.Events[7].Data[key] = wakeTimelineCursor
				}
				n.Events[7].Data["total_ms"], n.Events[7].Data["wait_ready_ms"] = float64(0), float64(0)
			case "wrong_app":
				n.AppID = "other"
				good = false
			case "wrong_wake":
				n.WakeID = "other"
				good = false
			case "wrong_limit":
				n.Limit = 50
				good = false
			case "over_limit":
				n.Events = append(n.Events, n.Events[7])
				good = false
			case "missing_app_id":
				delete(n.Events[7].Data, "app_id")
				good = false
			case "cross_app_row":
				n.Events[7].Data["app_id"] = "other"
				good = false
			case "cross_wake_row":
				n.Events[7].Data["wake_id"] = "other"
				good = false
			case "missing_data":
				n.Events[7].Data = nil
				good = false
			case "bad_at":
				n.Events[7].At = "bad"
				good = false
			case "unsorted":
				n.Events[7].At = n.Events[0].At
				good = false
			case "at_since":
				n.Events[0].At = wakeTimelineSince
				good = false
			case "bad_hint":
				n.NextCursor = "bad"
				good = false
			case "hint_at_since":
				n.NextCursor = wakeTimelineSince
				good = false
			case "empty_hint":
				n.NextCursor = wakeTimelineCursor
				good = false
			case "negative":
				n.Events[4].Data["elapsed_ms"] = float64(-1)
				good = false
			case "fractional":
				n.Events[4].Data["elapsed_ms"] = 0.5
				good = false
			case "overflow":
				n.Events[4].Data["elapsed_ms"] = float64(api.MCPWakeTimelineIntegerMax) + 1
				good = false
			case "nan":
				n.Events[4].Data["elapsed_ms"] = math.NaN()
				good = false
			case "inf":
				n.Events[4].Data["elapsed_ms"] = math.Inf(1)
				good = false
			case "string_number":
				n.Events[4].Data["elapsed_ms"] = "0"
				good = false
			case "wrong_bool":
				n.Events[2].Data["at_capacity"] = "false"
				good = false
			case "wrong_label":
				n.Events[2].Data["method"] = []any{"restore"}
				good = false
			case "wrong_producer_time":
				n.Events[3].Data["started_at"] = "bad"
				good = false
			case "wrong_time_type":
				n.Events[3].Data["started_at"] = 3
				good = false
			}
			v, err := (&management{opts: Options{Token: testToken}}).projectWakeTimeline(args, "app-1", n)
			if (err == nil) != good {
				t.Fatalf("good=%v error=%v", good, err)
			}
			if !good {
				return
			}
			if mode == "empty" && (v.Status != "no_returned_events" || len(v.Events) != 0 || v.NativeMoreEventsHint || v.StageObservations[0].Status != "not_returned" || v.SourceCompleteness != "unknown") {
				t.Fatal("empty filtered events fabricated stage outcomes")
			}
			if (mode == "legacy_missing" || mode == "null") && (v.Events[2].Details.QueuedCount != nil || v.Events[2].Details.AtCapacity != nil || v.Events[4].Details.ReadinessElapsedMS != nil) {
				t.Fatal("missing/null became observed zero/false")
			}
			if mode == "unknown_fallback" && (v.Events[3].Details.RestoreFallbackReason != "" || v.Events[3].Details.RestoreFallbackStatus != "unrecognized") {
				t.Fatal("unknown fallback leaked free text or became a known reason")
			}
			if mode == "repeated_kind" && v.StageObservations[4].EventCount != 2 {
				t.Fatal("repeated proxy events collapsed into one request")
			}
			if mode == "zero_producer_time" && v.Events[3].Details.StartedAt != "" {
				t.Fatal("zero producer time became observed phase start")
			}
			if mode == "boot_observed" && (v.Events[7].Details.ObservedAt != wakeTimelineCursor || v.Events[7].Details.Method != "restore") {
				t.Fatal("lost native boot observation")
			}
			if mode == "cold_boot_breakdown" && (v.Events[7].Details.NativeCallBasis != "cold_boot_call" || v.Events[7].Details.NativeCallTotalMS == nil || *v.Events[7].Details.NativeCallTotalMS != 0) {
				t.Fatal("cold-boot call boundary or observed zero lost")
			}
			if (mode == "boot_failed" || mode == "park_failed") && v.Events[7].Details.FailedAt != wakeTimelineCursor {
				t.Fatal("lost explicit failure event timestamp")
			}
			if (mode == "park_started" || mode == "park_completed" || mode == "park_failed") && (v.Events[7].Details.StartedAt != wakeTimelineCursor || v.Events[7].Stage != "parking") {
				t.Fatal("lost native parking observation")
			}
			encoded, _ := json.Marshal(v)
			if strings.Contains(string(encoded), "excluded-") || strings.Contains(string(encoded), testToken) {
				t.Fatal("selected stage details leaked excluded native fields")
			}
		})
	}
}

func TestWakeTimelineBoundsAndLabels(t *testing.T) {
	for _, limit := range []int{50, 100} {
		n := wakeTimelineFixture("app-1", limit, false)
		n.Events[2].Data["trigger"] = strings.Repeat("界", 200) + testToken
		for len(n.Events) < limit {
			n.Events = append(n.Events, n.Events[7])
		}
		v, err := (&management{opts: Options{Token: testToken}}).projectWakeTimeline(wakeTimelineArgs{App: "alpha", WakeID: "wake-1", Since: wakeTimelineSince, Limit: limit}, "app-1", n)
		if err != nil || v.ReturnedCount != limit || !v.TextTruncated || !v.Events[2].Details.LabelsModified || len(v.Events[2].Details.Trigger) > api.MCPRequestFieldMaxBytes {
			t.Fatalf("lost bounded label/event projection: %v", err)
		}
		encoded, _ := json.Marshal(v)
		if strings.Contains(string(encoded), testToken) {
			t.Fatal("credential crossed label truncation")
		}
	}
}

func TestWakeTimelineNativeErrorsAndIdentity(t *testing.T) {
	for _, mode := range []string{"unfiltered", "app_changed", "plan_gated", "denied", "missing", "unavailable", "cursor", "until", "on_behalf_of"} {
		t.Run(mode, func(t *testing.T) {
			var reads atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					id := "app-1"
					if mode == "app_changed" && reads.Load() > 0 {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/alpha/wakes/wake-1/timeline" || r.URL.Query().Get("since") != "" || r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("pagination") != "keyset_v1" || len(r.URL.Query()) != 2 {
					t.Error("unexpected read, automatic continuation or default filter")
				}
				reads.Add(1)
				status := map[string]int{"plan_gated": 402, "denied": 403, "missing": 404, "unavailable": 503}[mode]
				if status > 0 {
					api.WriteProblem(w, api.NewProblem(status, "native_wake_error", "Unavailable", "not available"))
					return
				}
				_ = json.NewEncoder(w).Encode(wakeTimelineFixture("app-1", 50, false))
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			args := map[string]any{"app": "alpha", "wake_id": "wake-1"}
			if mode == "cursor" || mode == "until" || mode == "on_behalf_of" {
				args[mode] = "other"
				r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_get_wake_timeline", Arguments: args})
				if err != nil || !r.IsError || reads.Load() != 0 {
					t.Fatal("unsupported argument reached native timeline")
				}
				return
			}
			out := callTool(t, ctx, cs, "gregale_get_wake_timeline", args, mode != "unfiltered")
			if reads.Load() != 1 || mode != "unfiltered" && out["events"] != nil {
				t.Fatal("unsafe result exposed, retried or automatically continued")
			}
			if mode != "unfiltered" && mode != "app_changed" && string(out["code"]) != `"native_wake_error"` {
				t.Fatal("native plan/MFA/retention error lost")
			}
		})
	}
}
