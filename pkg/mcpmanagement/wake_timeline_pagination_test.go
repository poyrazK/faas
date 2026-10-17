// adr: 1637
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const wakePageID = "wake-paged-1"
const wakeNativePage2 = "fixture-native-wake-page-2=="
const wakeNativePage3 = "fixture-native-wake-page-3=="

func wakePagedFixture(appID, cursor string) api.WakeTimelineResponse {
	n := wakeTimelineFixture(appID, 2, false)
	n.WakeID, n.NextCursor = wakePageID, ""
	p := &api.WakeTimelinePage{Mode: "keyset_v1", Since: wakeTimelineSince, Cursor: cursor, NextCursor: wakeNativePage2}
	rows := []api.WakeTimelineEvent{n.Events[0], n.Events[1]}
	switch cursor {
	case wakeNativePage2:
		rows = []api.WakeTimelineEvent{n.Events[2], n.Events[4]}
		p.NextCursor = wakeNativePage3
	case wakeNativePage3:
		rows = []api.WakeTimelineEvent{n.Events[5], n.Events[7]}
		p.NextCursor, p.Complete = "", true
	}
	for i := range rows {
		rows[i].At = wakeTimelineCursor
		rows[i].Data["wake_id"] = wakePageID
	}
	n.Page, n.Events = p, rows
	return n
}

func TestWakePaginationProtocol(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, approved := range []bool{false, true} {
			t.Run(version+map[bool]string{false: "/denied", true: "/approved"}[approved], func(t *testing.T) {
				var reads atomic.Int32
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/apps/alpha" {
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "alpha"})
						return
					}
					if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/alpha/wakes/"+wakePageID+"/timeline" {
						t.Error("unexpected page enrichment")
					}
					reads.Add(1)
					q := r.URL.Query()
					if q.Get("pagination") != "keyset_v1" || q.Get("since") != wakeTimelineSince || q.Get("limit") != "2" || strings.HasPrefix(q.Get("cursor"), "wp1.") {
						t.Error("changed query or forwarded MCP envelope to native")
					}
					_ = json.NewEncoder(w).Encode(wakePagedFixture("app-1", q.Get("cursor")))
				}))
				defer b.Close()
				opts := Options{APIBase: b.URL, Environments: &EnvironmentConsent{RequestReadApps: map[string]string{}}}
				if approved {
					opts.Environments.RequestReadApps["alpha"] = "app-1"
				}
				cs, ctx := connectConfiguredTestServer(t, b, opts, version)
				args := wakeTimelineArgs{App: "alpha", WakeID: wakePageID, Since: wakeTimelineSince, Limit: 2}
				kinds := []string{}
				for page := 0; page < 3; page++ {
					out := callTool(t, ctx, cs, "gregale_get_wake_timeline", args, !approved)
					if !approved {
						if reads.Load() != 0 {
							t.Fatal("paged diagnostic read bypassed consent")
						}
						return
					}
					var rows []wakeTimelineEventView
					_ = json.Unmarshal(out["events"], &rows)
					if len(rows) != 2 || reads.Load() != int32(page+1) || string(out["pagination_status"]) != `"keyset_at_id_v1"` || string(out["source_completeness"]) != `"unknown"` {
						t.Fatal("page bound/mode/completeness changed or automatic paging occurred")
					}
					for _, r := range rows {
						if r.At != wakeTimelineCursor {
							t.Fatal("tied native timestamp altered")
						}
						kinds = append(kinds, r.Kind)
					}
					if page == 2 {
						if string(out["page_complete"]) != "true" || out["next_arguments"] != nil || out["next_cursor"] != nil {
							t.Fatal("terminal retained page invented continuation")
						}
						break
					}
					var next wakeTimelineArgs
					if json.Unmarshal(out["next_arguments"], &next) != nil || next.App != args.App || next.WakeID != args.WakeID || next.Since != args.Since || next.Limit != args.Limit || !strings.HasPrefix(next.Cursor, "wp1.") {
						t.Fatal("continuation selection changed")
					}
					args = next
				}
				if len(kinds) != 6 || kinds[2] != "wake.boot_started" || kinds[4] != "wake.proxy_first_byte" {
					t.Fatal("equal timestamp boundary events lost")
				}
			})
		}
	}
}

func TestWakePaginationGuards(t *testing.T) {
	m := &management{}
	args := wakeTimelineArgs{App: "alpha", WakeID: wakePageID, Since: wakeTimelineSince, Limit: 2}
	first, err := m.projectWakeTimeline(args, "app-1", wakePagedFixture("app-1", ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"app", "wake", "since", "limit", "app_id", "legacy_downgrade", "echo_cursor", "echo_since", "mode", "complete", "empty_more", "no_progress", "backwards", "mixed_legacy", "unsafe_native", "valid_tie", "empty_terminal"} {
		t.Run(mode, func(t *testing.T) {
			a := *first.NextArguments
			n := wakePagedFixture("app-1", wakeNativePage2)
			appID := "app-1"
			switch mode {
			case "app":
				a.App = "beta"
			case "wake":
				a.WakeID = "other"
			case "since":
				a.Since = "2026-10-16T10:00:01Z"
			case "limit":
				a.Limit = 3
			case "app_id":
				appID = "replacement"
			case "legacy_downgrade":
				n.Page = nil
			case "echo_cursor":
				n.Page.Cursor = "other"
			case "echo_since":
				n.Page.Since = ""
			case "mode":
				n.Page.Mode = "unknown"
			case "complete":
				n.Page.Complete = true
			case "empty_more":
				n.Events = nil
			case "no_progress":
				n.Page.NextCursor = n.Page.Cursor
			case "backwards":
				n.Events[0].At = "2026-10-16T10:59:00Z"
			case "mixed_legacy":
				n.NextCursor = wakeTimelineCursor
			case "unsafe_native":
				n.Page.NextCursor = testToken
			case "empty_terminal":
				n.Events = nil
				n.Page.Complete, n.Page.NextCursor = true, ""
			}
			_, e := m.projectWakeTimeline(a, appID, n)
			good := mode == "valid_tie" || mode == "empty_terminal"
			if (e == nil) != good {
				t.Fatalf("mode %s error=%v", mode, e)
			}
		})
	}
}

func TestWakePaginationAppReplacement(t *testing.T) {
	var changed atomic.Bool
	var reads atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha" {
			id := "app-1"
			if changed.Load() {
				id = "replacement"
			}
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "alpha"})
			return
		}
		reads.Add(1)
		_ = json.NewEncoder(w).Encode(wakePagedFixture("app-1", r.URL.Query().Get("cursor")))
	}))
	defer b.Close()
	cs, ctx := connectTestServer(t, b, 0, "2026-07-28")
	first := callTool(t, ctx, cs, "gregale_get_wake_timeline", wakeTimelineArgs{App: "alpha", WakeID: wakePageID, Since: wakeTimelineSince, Limit: 2}, false)
	var next wakeTimelineArgs
	_ = json.Unmarshal(first["next_arguments"], &next)
	changed.Store(true)
	out := callTool(t, ctx, cs, "gregale_get_wake_timeline", next, true)
	if reads.Load() != 1 || out["events"] != nil {
		t.Fatal("old continuation authorized recreated app")
	}
}
