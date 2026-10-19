// adr: 1633
package mcpmanagement

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestErrorGroupProjectionGuards(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "clamped", "foreign_slug", "missing_time", "widened", "changed_until", "hidden_clamp", "false_clamp", "changed_continuation_window", "bad_limit", "extra_rows", "bad_fingerprint", "negative_count", "negative_requests", "bad_class", "bad_status", "reversed_dates", "last_outside", "duplicate", "out_of_order", "same_count_tie", "secret_fingerprint"} {
		t.Run(mode, func(t *testing.T) {
			m := &management{}
			opts, c, err := m.errorGroupsSelection(errorGroupsArgs{App: "alpha", Since: errorSince, Until: errorUntil})
			if err != nil {
				t.Fatal(err)
			}
			n := errorGroupsFixture("app-1", "", opts.Limit, false)
			switch mode {
			case "empty":
				n.Items = nil
			case "clamped":
				opts.Since = "2026-10-15T10:00:00Z"
				n.WindowClamped = true
			case "foreign_slug":
				n.AppSlug = "other"
			case "missing_time":
				n.GeneratedAt = ""
			case "widened":
				n.WindowStart = "2026-10-16T09:00:00Z"
			case "changed_until":
				n.WindowEnd = "2026-10-16T10:59:00Z"
			case "hidden_clamp":
				n.WindowStart = "2026-10-16T10:30:00Z"
			case "false_clamp":
				n.WindowClamped = true
			case "changed_continuation_window":
				opts.Since = "2026-10-16T09:00:00Z"
				c.Native, c.WindowStart, c.WindowEnd = errorNativeGroupsNext, errorSince, errorUntil
				n.WindowStart, n.WindowClamped = "2026-10-16T10:30:00Z", true
			case "bad_limit":
				n.Limit = 19
			case "extra_rows":
				opts.Limit, n.Limit = 1, 1
			case "bad_fingerprint":
				n.Items[0].Fingerprint = strings.Repeat("B", 64)
			case "negative_count":
				n.Items[0].Count = -1
			case "negative_requests":
				n.Items[0].RequestCount = -1
			case "bad_class":
				n.Items[0].ErrorClass = "excluded-untrusted-prose"
			case "bad_status":
				n.Items[0].HTTPStatus = 200
			case "reversed_dates":
				n.Items[0].FirstSeenAt = "2026-10-16T11:01:00Z"
			case "last_outside":
				n.Items[0].LastSeenAt = "2026-10-16T09:59:00Z"
			case "duplicate":
				n.Items[1] = n.Items[0]
			case "out_of_order":
				n.Items[0], n.Items[1] = n.Items[1], n.Items[0]
			case "same_count_tie":
				n.Items[1].Count = n.Items[0].Count
			case "secret_fingerprint":
				m.opts.Token = errorFixtureFingerprint()
			}
			good := mode == "valid" || mode == "empty" || mode == "clamped" || mode == "same_count_tie"
			v, err := m.projectErrorGroups("alpha", "app-1", opts, c, n)
			if (err == nil) != good {
				t.Fatalf("projection=%+v err=%v", v, err)
			}
			if mode == "same_count_tie" && v.Groups[1].Fingerprint >= v.Groups[0].Fingerprint {
				t.Fatal("fingerprint DESC tie-break lost")
			}
		})
	}
}

func TestErrorRequestProjectionGuards(t *testing.T) {
	for _, mode := range []string{"valid", "terminal", "first_empty", "bad_fingerprint", "bad_header", "bad_row_class", "bad_row_status", "bad_row_route", "unsafe_lookup", "nil_lookup", "rewritten_lookup", "bad_time", "out_of_order", "duplicate_tuple", "repeated_id_other_time", "extra_rows", "secret_lookup"} {
		t.Run(mode, func(t *testing.T) {
			m := &management{}
			args := errorRequestsArgs{App: "alpha", Fingerprint: errorFixtureFingerprint(), Limit: 20}
			c := errorPageCursor{Version: 1, Kind: "requests", App: "alpha", Fingerprint: args.Fingerprint, Limit: 20}
			n := errorRequestsFixture("", 20)
			switch mode {
			case "terminal":
				c.Native = errorNativeRequestsNext
				n = errorRequestsFixture(c.Native, 20)
			case "first_empty":
				n = errorRequestsFixture(errorNativeRequestsNext, 20)
			case "bad_fingerprint":
				n.Fingerprint = strings.Repeat("a", 64)
			case "bad_header":
				n.ErrorClass = ""
			case "bad_row_class":
				n.Requests[0].ErrorClass = "client_error"
			case "bad_row_status":
				n.Requests[0].HTTPStatus = 500
			case "bad_row_route":
				n.Requests[0].Route = "/other"
			case "unsafe_lookup":
				n.Requests[0].RequestID = "../debug"
			case "nil_lookup":
				n.Requests[0].RequestID = "00000000-0000-0000-0000-000000000000"
			case "rewritten_lookup":
				n.Requests[0].RequestID = strings.ToUpper(errorRequestID)
			case "bad_time":
				n.Requests[0].ReceivedAt = "bad-time"
			case "out_of_order", "duplicate_tuple", "repeated_id_other_time", "extra_rows":
				row := n.Requests[0]
				if mode == "out_of_order" {
					row.ReceivedAt = "2026-10-16T08:00:00Z"
				}
				if mode == "repeated_id_other_time" || mode == "extra_rows" {
					row.ReceivedAt = "2026-10-14T08:00:00Z"
				}
				n.Requests = append(n.Requests, row)
				if mode == "extra_rows" {
					args.Limit = 1
				}
			case "secret_lookup":
				m.opts.Token = errorRequestID
			}
			good := mode == "valid" || mode == "terminal" || mode == "repeated_id_other_time"
			v, err := m.projectErrorRequests(args, "app-1", args.Limit, c, n)
			if (err == nil) != good {
				t.Fatalf("projection=%+v err=%v", v, err)
			}
			if mode == "terminal" && (v.HeaderStatus != "not_reported" || !v.PageComplete || len(v.Requests) != 0) {
				t.Fatal("empty terminal page invented header data")
			}
		})
	}
}

func TestErrorCursorOutputBounds(t *testing.T) {
	m := &management{}
	c := errorPageCursor{Version: 1, Kind: "requests", App: "alpha", AppID: "app-1", Fingerprint: errorFixtureFingerprint(), Limit: 1}
	for _, next := range []string{testToken, "native\ncursor", strings.Repeat("x", api.MCPErrorCursorMaxBytes)} {
		if _, err := m.writeErrorCursor(c, next); err == nil {
			t.Fatal("unsafe or oversized cursor returned")
		}
	}
	c.Native = errorNativeRequestsNext
	if _, err := m.writeErrorCursor(c, c.Native); err == nil {
		t.Fatal("native cursor made no progress")
	}
}
