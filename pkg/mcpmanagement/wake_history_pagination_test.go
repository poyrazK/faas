// adr: 1638
package mcpmanagement

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const historyPageSince = "2026-10-15T10:00:00Z"
const historyPageUntil = "2026-10-15T11:00:00Z"
const historyPageAt = "2026-10-15T10:30:00.123456Z"
const historyNative2 = "fixture-history-page-2=="
const historyNative3 = "fixture-history-page-3=="

func historyPagedFixture(appID, raw string) api.AppWakeTimelineResponse {
	start, next, complete := 0, historyNative2, false
	if raw == historyNative2 {
		start, next = 2, historyNative3
	}
	if raw == historyNative3 {
		start, next, complete = 4, "", true
	}
	n := api.AppWakeTimelineResponse{App: api.WakeTimelineApp{AppID: appID, Slug: "alpha"}, AsOf: "2026-10-17T10:00:00Z", WakeCount24h: 2, Rows: []api.WakeTimelineJSONRow{}, Page: &api.AppWakeHistoryPage{Mode: "keyset_v1", Since: historyPageSince, Until: historyPageUntil, Limit: 2, Cursor: raw, NextCursor: next, Complete: complete, SourceStatus: "read_succeeded", CountScope: "returned_page"}}
	for _, id := range []string{"history-wake-1", "history-wake-2", "history-wake-3", "history-wake-4", "history-wake-5", "history-wake-6"}[start : start+2] {
		n.Rows = append(n.Rows, api.WakeTimelineJSONRow{WakeID: id, Kind: "wake.boot_started", At: historyPageAt, State: "running", ReadyInMS: -1})
	}
	return n
}

func TestWakeHistoryPaginationProtocol(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, approved := range []bool{false, true} {
			t.Run(version+map[bool]string{false: "/denied", true: "/approved"}[approved], func(t *testing.T) {
				var reads atomic.Int32
				var replaced atomic.Bool
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/apps/alpha" {
						id := "app-1"
						if replaced.Load() {
							id = "replacement"
						}
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
						return
					}
					reads.Add(1)
					q := r.URL.Query()
					if r.Method != "GET" || r.URL.Path != "/v1/apps/alpha/wake-timeline" || q.Get("pagination") != "keyset_v1" || q.Get("since") != historyPageSince || q.Get("until") != historyPageUntil || q.Get("limit") != "2" || strings.HasPrefix(q.Get("cursor"), "hp1.") {
						t.Error("changed window or forwarded MCP cursor")
					}
					_ = json.NewEncoder(w).Encode(historyPagedFixture("app-1", q.Get("cursor")))
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL, Environments: &EnvironmentConsent{RequestReadApps: map[string]string{}}}
				if approved {
					opts.Environments.RequestReadApps["alpha"] = "app-1"
				}
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				args := wakeHistoryArgs{App: "alpha", Since: historyPageSince, Until: historyPageUntil, Limit: 2}
				var saved wakeHistoryArgs
				ids := map[string]bool{}
				for page := 0; page < 3; page++ {
					out := callTool(t, ctx, cs, "gregale_get_wake_history", args, !approved)
					if !approved {
						if reads.Load() != 0 {
							t.Fatal("denied history read reached native API")
						}
						return
					}
					var v wakeHistoryView
					raw, _ := json.Marshal(out)
					if json.Unmarshal(raw, &v) != nil || v.PaginationStatus != "keyset_started_at_instance_id_v1" || v.SourceStatus != "read_succeeded" || v.CountBasis != "returned_page_instance_records" || v.SourceCompleteness != "unknown" || v.NativeCandidateLimit != 0 || len(v.Wakes) != 2 || reads.Load() != int32(page+1) {
						t.Fatal("lost page source, counts or bounds")
					}
					for _, row := range v.Wakes {
						if row.At != historyPageAt || ids[row.WakeID] || row.WindowMembership != "verified_native_window" {
							t.Fatal("lost tied-time record")
						}
						ids[row.WakeID] = true
					}
					if page == 2 {
						if v.PageComplete == nil || !*v.PageComplete || v.NextArguments != nil {
							t.Fatal("terminal continuation invented")
						}
						break
					}
					if v.NextArguments == nil || v.NextArguments.Since != args.Since || v.NextArguments.Until != args.Until || !strings.HasPrefix(v.NextArguments.Cursor, "hp1.") {
						t.Fatal("lost exact original selection")
					}
					args = *v.NextArguments
					if page == 0 {
						saved = args
					}
				}
				replaced.Store(true)
				callTool(t, ctx, cs, "gregale_get_wake_history", saved, true)
				if reads.Load() != 3 {
					t.Fatal("recreated app read using old continuation")
				}
			})
		}
	}
}

func TestWakeHistoryPaginationGuards(t *testing.T) {
	m := &management{}
	args := wakeHistoryArgs{App: "alpha", Since: historyPageSince, Until: historyPageUntil, Limit: 2}
	n := historyPagedFixture("app-1", "")
	opts := api.AppWakeTimelineOptions{Since: args.Since, Until: args.Until}
	v, err := m.projectWakeHistory(args.App, "app-1", opts, 2, n)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.projectHistoryPagination(args, "app-1", n, &v); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"app", "since", "until", "limit", "identity", "echo", "window", "source", "counts", "complete", "no_progress", "backwards", "downgrade", "valid_tie", "empty_terminal"} {
		t.Run(mode, func(t *testing.T) {
			a := *v.NextArguments
			n := historyPagedFixture("app-1", historyNative2)
			id := "app-1"
			switch mode {
			case "app":
				a.App = "other"
			case "since":
				a.Since = "2026-10-15T10:01:00Z"
			case "until":
				a.Until = "2026-10-15T10:59:00Z"
			case "limit":
				a.Limit = 3
			case "identity":
				id = "replacement"
			case "echo":
				n.Page.Cursor = "other"
			case "window":
				n.Page.Until = ""
			case "source":
				n.Page.SourceStatus = "unavailable"
			case "counts":
				n.Page.CountScope = "all_wakes"
			case "complete":
				n.Page.Complete = true
			case "no_progress":
				n.Page.NextCursor = n.Page.Cursor
			case "backwards":
				n.Rows[0].At = "2026-10-15T10:31:00Z"
			case "downgrade":
				n.Page = nil
			case "empty_terminal":
				n.Rows = nil
				n.Page.Complete = true
				n.Page.NextCursor = ""
			}
			view := wakeHistoryView{}
			e := m.projectHistoryPagination(a, id, n, &view)
			good := mode == "valid_tie" || mode == "empty_terminal"
			if (e == nil) != good {
				t.Fatalf("mode %s: %v", mode, e)
			}
		})
	}
}
