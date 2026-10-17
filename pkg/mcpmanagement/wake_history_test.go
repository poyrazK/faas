// adr: 1634
package mcpmanagement

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const wakeHistorySince = "2026-10-16T10:00:00Z"
const wakeHistoryUntil = "2026-10-16T11:00:00Z"

func wakeHistoryFixture(appID string, empty bool) api.AppWakeTimelineResponse {
	n := api.AppWakeTimelineResponse{
		App:  api.WakeTimelineApp{AppID: appID, Slug: "alpha", Status: "excluded-status", URL: "https://excluded-private.example"},
		AsOf: "2026-10-16T11:00:01Z", WakeCount24h: 3, WakeCountWithMeta: 2, AtCapacityCount: 1, AtCapacityPct: 50,
		TriggerHistogram: map[string]int{"excluded-histogram": 99}, TriggerClassHistogram: map[string]int{"excluded-class": 99},
		Rows: []api.WakeTimelineJSONRow{
			{WakeID: "wake-1", Kind: "wake.boot_started", State: "RUNNING", At: "2026-10-16T10:59:00Z", Trigger: "http " + testToken, TriggerClass: "monitor", Method: "restore", Tier: "warm", QueuedCount: 2, ConcurrencyAtAdmit: 2, AtCapacityPresent: true, AtCapacity: true, ReadyInMS: 112},
			{WakeID: "wake-2", Kind: "wake.boot_started", State: "WAKING", At: "2026-10-16T10:30:00Z", Trigger: "cron", Method: "cold_boot", Tier: "cold_boot_fallback", AtCapacityPresent: true, ReadyInMS: -1},
			{Kind: "wake.boot_started", State: "RUNNING", At: wakeHistorySince, ReadyInMS: -1},
		},
	}
	if empty {
		n.Rows = nil
		n.WakeCount24h, n.WakeCountWithMeta, n.AtCapacityCount, n.AtCapacityPct = 0, 0, 0, 0
	}
	return n
}

func TestWakeHistoryConsentAndProjection(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"local", "approved", "denied", "replaced", "allowlist_denied"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("wake history wrote native state")
						w.WriteHeader(405)
						return
					}
					switch r.URL.Path {
					case "/v1/apps/alpha":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
					case "/v1/apps/alpha/wake-timeline":
						reads.Add(1)
						q := r.URL.Query()
						if q.Get("since") != wakeHistorySince || q.Get("until") != wakeHistoryUntil || len(q) != 4 || q.Get("pagination") != "keyset_v1" || q.Get("limit") != "2" {
							t.Error("changed requested wake window or forwarded output limit")
						}
						_ = json.NewEncoder(w).Encode(wakeHistoryFixture("app-1", false))
					default:
						t.Errorf("unexpected wake enrichment %s", r.URL.Path)
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
				args := wakeHistoryArgs{App: "alpha", Since: wakeHistorySince, Until: wakeHistoryUntil, Limit: 2}
				out := callTool(t, ctx, cs, "gregale_get_wake_history", args, denied)
				if denied {
					if reads.Load() != 0 || mode != "allowlist_denied" && string(out["code"]) != `"request_access_denied"` {
						t.Fatal("wake read bypassed consent")
					}
					return
				}
				encoded, _ := json.Marshal(out)
				var v wakeHistoryView
				_ = json.Unmarshal(encoded, &v)
				if reads.Load() != 1 || v.AppID != "app-1" || v.Scope != "app_wide" || v.SourceStatus != "not_reported" || v.SourceCompleteness != "unknown" || v.Freshness != "unknown" || v.WindowVerification != "not_echoed_by_native" || v.RequestedSince != args.Since || v.RequestedUntil != args.Until || v.NativeCandidateLimit != 100 || v.CandidateTruncation != "not_reported" {
					t.Fatal("lost identity, requested range or native uncertainty")
				}
				if v.AdmissionCountBasis != "native_admission_concurrency_ledger" || v.NativeSelectedCount != 3 || v.NativeCount != 3 || v.ReturnedCount != 2 || !v.RowsTruncated || len(v.Wakes) != 2 || v.CapacityPresentCount != 2 || v.AtCapacityPct == nil || *v.AtCapacityPct != 50 || v.AtCapacityPctStatus != "observed_on_meta_rows" {
					t.Fatal("lost native counts, denominator or output bounds")
				}
				first, second := v.Wakes[0], v.Wakes[1]
				if first.ReadyInMS == nil || *first.ReadyInMS != 112 || first.QueuedCount == nil || *first.QueuedCount != 2 || first.ConcurrencyAtAdmit == nil || *first.ConcurrencyAtAdmit != 2 || !first.LabelsModified || second.AtCapacity == nil || *second.AtCapacity || second.QueuedCount != nil || second.ConcurrencyAtAdmit != nil || second.QueuedCountStatus != "zero_or_not_reported" || second.ReadyInMS != nil || second.ReadinessStatus != "not_reported" {
					t.Fatal("confused observed false, missing scalar presence or unknown readiness")
				}
				for _, marker := range []string{"excluded-", testToken, `"request_id"`, `"instance_id"`, `"node_id"`, `"trigger_histogram"`} {
					if strings.Contains(string(encoded), marker) {
						t.Errorf("wake projection leaked %s", marker)
					}
				}
				if mode == "approved" {
					args.App = "future"
					callTool(t, ctx, cs, "gregale_get_wake_history", args, true)
					if reads.Load() != 1 {
						t.Fatal("wake consent widened to a future app")
					}
				}
			})
		}
	}
}

func TestWakeHistorySelections(t *testing.T) {
	for _, tc := range []struct {
		name string
		args wakeHistoryArgs
		bad  bool
	}{
		{name: "defaults"},
		{name: "absolute", args: wakeHistoryArgs{Since: "2026-10-16T12:00:00.123456789+02:00", Until: "2026-10-16T13:00:00+02:00", Limit: 50}},
		{name: "until_only", args: wakeHistoryArgs{Until: wakeHistoryUntil}},
		{name: "relative", args: wakeHistoryArgs{Since: "1h"}, bad: true},
		{name: "bad_until", args: wakeHistoryArgs{Until: "garbage"}, bad: true},
		{name: "equal", args: wakeHistoryArgs{Since: wakeHistorySince, Until: wakeHistorySince}, bad: true},
		{name: "reversed", args: wakeHistoryArgs{Since: wakeHistoryUntil, Until: wakeHistorySince}, bad: true},
		{name: "future", args: wakeHistoryArgs{Until: time.Now().Add(time.Hour).Format(time.RFC3339)}, bad: true},
		{name: "controls", args: wakeHistoryArgs{Since: wakeHistorySince + "\n"}, bad: true},
		{name: "long", args: wakeHistoryArgs{Since: strings.Repeat("x", 65)}, bad: true},
		{name: "negative", args: wakeHistoryArgs{Limit: -1}, bad: true},
		{name: "too_many", args: wakeHistoryArgs{Limit: 51}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.args.App = "alpha"
			opts, limit, err := wakeHistorySelection(tc.args)
			if (err != nil) != tc.bad {
				t.Fatalf("selection error=%v", err)
			}
			if tc.bad {
				return
			}
			from, _ := time.Parse(time.RFC3339Nano, opts.Since)
			until, _ := time.Parse(time.RFC3339Nano, opts.Until)
			if limit < 1 || limit > 50 || tc.args.Limit == 0 && limit != 20 || tc.args.Since == "" && until.Sub(from) != 24*time.Hour || tc.name == "absolute" && opts.Since != "2026-10-16T10:00:00.123456Z" {
				t.Fatal("defaults or timestamp precision changed")
			}
		})
	}
}

func TestWakeHistoryNativeValidation(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "unknown_capacity", "no_meta", "missing_started_at", "duplicate_id", "zero_readiness", "subsecond_boundary", "inclusive_until", "too_many", "wrong_id", "wrong_slug", "bad_as_of", "wrong_count", "negative_meta", "too_much_meta", "negative_capacity", "wrong_capacity", "bad_pct", "nan_pct", "wrong_pct", "unsorted", "bad_kind", "bad_at", "fractional_at", "before_since", "after_until", "negative_queue", "negative_concurrency", "bad_ready", "unsafe_id", "secret_id", "long_id", "metadata_count", "omitted_invalid"} {
		t.Run(mode, func(t *testing.T) {
			n := wakeHistoryFixture("app-1", mode == "empty")
			opts := api.AppWakeTimelineOptions{Since: wakeHistorySince, Until: wakeHistoryUntil}
			good := true
			switch mode {
			case "unknown_capacity":
				n.Rows[1].AtCapacityPresent, n.Rows[1].AtCapacity = false, true
			case "no_meta":
				for i := range n.Rows {
					n.Rows[i] = api.WakeTimelineJSONRow{Kind: "wake.boot_started", At: n.Rows[i].At, ReadyInMS: -1}
				}
				n.WakeCountWithMeta, n.AtCapacityCount, n.AtCapacityPct = 0, 0, 0
			case "missing_started_at":
				n.Rows[2].At = ""
			case "duplicate_id":
				n.Rows[1].WakeID = n.Rows[0].WakeID
			case "zero_readiness":
				n.Rows[1].ReadyInMS = 0
			case "subsecond_boundary":
				opts.Since = "2026-10-16T10:00:00.500Z"
			case "inclusive_until":
				n.Rows[0].At = wakeHistoryUntil
			case "too_many":
				for len(n.Rows) <= 100 {
					n.Rows = append(n.Rows, n.Rows[2])
				}
				n.WakeCount24h = len(n.Rows)
				good = false
			case "wrong_id":
				n.App.AppID = "other"
				good = false
			case "wrong_slug":
				n.App.Slug = "other"
				good = false
			case "bad_as_of":
				n.AsOf = ""
				good = false
			case "wrong_count":
				n.WakeCount24h = 4
				good = false
			case "negative_meta":
				n.WakeCountWithMeta = -1
				good = false
			case "too_much_meta":
				n.WakeCountWithMeta = 4
				good = false
			case "negative_capacity":
				n.AtCapacityCount = -1
				good = false
			case "wrong_capacity":
				n.AtCapacityCount = 0
				good = false
			case "bad_pct":
				n.AtCapacityPct = 101
				good = false
			case "nan_pct":
				n.AtCapacityPct = math.NaN()
				good = false
			case "wrong_pct":
				n.AtCapacityPct = 100
				good = false
			case "unsorted":
				n.Rows[2].At = "2026-10-16T10:58:00Z"
				good = false
			case "bad_kind":
				n.Rows[2].Kind = "wake.ready"
				good = false
			case "bad_at":
				n.Rows[2].At = "n/a"
				good = false
			case "fractional_at":
				n.Rows[2].At = "2026-10-16T10:00:00.1Z"
				good = false
			case "before_since":
				n.Rows[2].At = "2026-10-16T09:59:59Z"
				good = false
			case "after_until":
				n.Rows[0].At = "2026-10-16T11:00:01Z"
				good = false
			case "negative_queue":
				n.Rows[2].QueuedCount = -1
				good = false
			case "negative_concurrency":
				n.Rows[2].ConcurrencyAtAdmit = -1
				good = false
			case "bad_ready":
				n.Rows[2].ReadyInMS = -2
				good = false
			case "unsafe_id":
				n.Rows[2].WakeID = "../private"
				good = false
			case "secret_id":
				n.Rows[2].WakeID = testToken
				good = false
			case "long_id":
				n.Rows[2].WakeID = strings.Repeat("x", 129)
				good = false
			case "metadata_count":
				n.Rows[2].QueuedCount = 1
				good = false
			case "omitted_invalid":
				n.Rows[2].AtCapacityPresent = true
				good = false
			}
			v, err := (&management{opts: Options{Token: testToken}}).projectWakeHistory("alpha", "app-1", opts, 2, n)
			if (err == nil) != good {
				t.Fatalf("validation good=%v err=%v", good, err)
			}
			if !good {
				return
			}
			if mode == "empty" && (v.Status != "no_returned_wakes" || len(v.Wakes) != 0 || v.AtCapacityPct != nil || v.AtCapacityPctStatus != "no_meta_rows" || v.SourceStatus != "not_reported") {
				t.Fatal("empty history fabricated health/completeness or capacity")
			}
			if mode == "unknown_capacity" && (v.Wakes[1].AtCapacity != nil || v.CapacityUnknownInMeta != 1 || v.CapacityPresentCount != 1 || v.AtCapacityPctStatus != "native_denominator_includes_unknown_capacity") {
				t.Fatal("missing capacity presence became observed false/true")
			}
			if mode == "no_meta" && (v.AtCapacityPct != nil || v.AtCapacityPctStatus != "no_meta_rows") {
				t.Fatal("missing metadata became a zero percent measurement")
			}
			if mode == "missing_started_at" {
				v, err = (&management{}).projectWakeHistory("alpha", "app-1", opts, 3, n)
				if err != nil || v.Wakes[2].WindowMembership != "unverified_missing_started_at" || v.Wakes[2].WakeIDStatus != "not_reported" {
					t.Fatal("legacy missing identity/time became verified")
				}
			}
		})
	}
}

func TestWakeHistoryBoundsAndLabels(t *testing.T) {
	n := wakeHistoryFixture("app-1", false)
	for len(n.Rows) < 100 {
		n.Rows = append(n.Rows, n.Rows[2])
	}
	n.WakeCount24h = len(n.Rows)
	n.Rows[0].Tier = strings.Repeat("界", 200) + testToken
	n.Rows[0].State += "\n" + testToken
	opts := api.AppWakeTimelineOptions{Since: wakeHistorySince, Until: wakeHistoryUntil}
	for _, limit := range []int{1, api.MCPWakeHistoryDefault, api.MCPWakeHistoryMax} {
		v, err := (&management{opts: Options{Token: testToken}}).projectWakeHistory("alpha", "app-1", opts, limit, n)
		if err != nil || !v.TextTruncated || !v.RowsTruncated || v.ReturnedCount != limit || v.NativeCount != 100 || v.CapacityPresentCount != 2 || !v.Wakes[0].LabelsModified || len(v.Wakes[0].Tier) > api.MCPRequestFieldMaxBytes {
			t.Fatalf("bounds or full-response validation lost: %v", err)
		}
		encoded, _ := json.Marshal(v)
		if strings.Contains(string(encoded), testToken) {
			t.Fatal("bounded label leaked credential")
		}
	}
}

func TestWakeHistoryNativeErrorsAndIdentity(t *testing.T) {
	for _, mode := range []string{"app_changed", "plan_gated", "native_denied", "native_error", "unknown_argument", "operator_argument"} {
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
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/alpha/wake-timeline" {
					t.Error("extra native wake request")
				}
				reads.Add(1)
				status := map[string]int{"plan_gated": 402, "native_denied": 403, "native_error": 503}[mode]
				if status > 0 {
					api.WriteProblem(w, api.NewProblem(status, "native_wake_error", "Unavailable", "wake history unavailable"))
					return
				}
				_ = json.NewEncoder(w).Encode(wakeHistoryFixture("app-1", false))
			}))
			defer b.Close()
			cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
			args := map[string]any{"app": "alpha", "since": wakeHistorySince, "until": wakeHistoryUntil}
			if mode == "unknown_argument" || mode == "operator_argument" {
				key := "wake_id"
				if mode == "operator_argument" {
					key = "on_behalf_of"
				}
				args[key] = "other"
				r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_get_wake_history", Arguments: args})
				if err != nil || !r.IsError || reads.Load() != 0 {
					t.Fatal("unsupported selector bypassed schema")
				}
				return
			}
			out := callTool(t, ctx, cs, "gregale_get_wake_history", args, true)
			if reads.Load() != 1 || out["wakes"] != nil {
				t.Fatal("identity/native error exposed evidence or retried")
			}
			if mode != "app_changed" && string(out["code"]) != `"native_wake_error"` {
				t.Fatal("native entitlement/error code lost")
			}
		})
	}
}
