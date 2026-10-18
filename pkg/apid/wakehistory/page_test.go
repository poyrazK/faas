// adr: 1638
package wakehistory

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type historySource struct {
	rows []state.WakeHistoryRecord
	err  error
}

func (s historySource) ListAppWakeHistoryPage(_ context.Context, _ string, _ time.Time, _ time.Time, _ state.WakeHistoryPosition, _ int) ([]state.WakeHistoryRecord, error) {
	return s.rows, s.err
}

func TestHistoryPageContract(t *testing.T) {
	app := state.App{ID: "app-1", Slug: "alpha"}
	at := time.Date(2026, 10, 16, 10, 0, 0, 123456000, time.UTC)
	q := url.Values{"pagination": {"keyset_v1"}, "since": {"2026-10-16T10:00:00Z"}, "until": {"2026-10-16T11:00:00Z"}, "limit": {"2"}}
	rows := []state.WakeHistoryRecord{}
	for _, id := range []string{"00000000-0000-0000-0000-000000000003", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000001"} {
		rows = append(rows, state.WakeHistoryRecord{InstanceID: id, AppID: app.ID, WakeID: "wake-1", State: "running", StartedAt: at})
	}
	first, p := ReadPage(t.Context(), historySource{rows: rows}, "acct", app, q)
	if p != nil || first.Page == nil || first.Page.Complete || len(first.Rows) != 2 || first.Page.SourceStatus != "read_succeeded" || first.WakeCount24h != 2 {
		t.Fatalf("first page: %+v %v", first, p)
	}
	for _, mode := range []string{"account", "app", "since", "until", "limit", "malformed", "unavailable", "invalid_source", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			args := url.Values{}
			for k, v := range q {
				args[k] = append([]string(nil), v...)
			}
			args.Set("cursor", first.Page.NextCursor)
			acct, a := "acct", app
			s := historySource{rows: rows[2:]}
			var store any = s
			switch mode {
			case "account":
				acct = "other"
			case "app":
				a.ID = "other"
			case "since":
				args.Set("since", "2026-10-16T10:00:00.1Z")
			case "until":
				args.Set("until", "2026-10-16T10:59:59Z")
			case "limit":
				args.Set("limit", "3")
			case "malformed":
				args.Set("cursor", "garbage")
			case "unavailable":
				s.err = errors.New("private backend failure")
				store = s
			case "invalid_source":
				s.rows = rows
				store = s
			case "unsupported":
				store = struct{}{}
			}
			out, p := ReadPage(t.Context(), store, acct, a, args)
			if p == nil || len(out.Rows) > 0 && mode != "invalid_source" {
				t.Fatal("invalid context/source presented as a successful empty page")
			}
			if mode == "unavailable" && p.Status != 503 {
				t.Fatal("source failure swallowed")
			}
		})
	}
	q.Set("cursor", first.Page.NextCursor)
	last, p := ReadPage(t.Context(), historySource{rows: rows[2:]}, "acct", app, q)
	if p != nil || !last.Page.Complete || len(last.Rows) != 1 || last.Rows[0].At != first.Rows[1].At {
		t.Fatal("tie lost at boundary")
	}
	empty, p := ReadPage(t.Context(), historySource{}, "acct", app, q)
	if p != nil || !empty.Page.Complete || empty.Page.SourceStatus != "read_succeeded" || len(empty.Rows) != 0 {
		t.Fatal("empty terminal page not distinguished from source failure")
	}
}
