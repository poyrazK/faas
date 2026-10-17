// adr: 1637
package waketimeline

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestReadPageTiesAndContext(t *testing.T) {
	store := state.NewMemStore()
	at := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	for _, kind := range []string{"wake.queue_accepted", "wake.admitted", "wake.boot_started", "wake.boot_completed", "wake.proxy_first_byte"} {
		for _, app := range []string{"foreign", "app-1"} {
			raw, _ := json.Marshal(map[string]string{"app_id": app, "wake_id": "wake-1"})
			if err := store.AppendEventAt(t.Context(), "schedd", kind, nil, raw, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := url.Values{"pagination": {"keyset_v1"}, "limit": {"2"}}
	first, problem := ReadPage(t.Context(), store, "account-1", "app-1", "wake-1", q)
	if problem != nil || len(first.Events) != 2 || first.Page == nil || first.Page.Complete || first.Page.NextCursor == "" || first.NextCursor != "" {
		t.Fatalf("invalid first page: %+v %v", first, problem)
	}
	for _, mode := range []string{"app", "account", "wake", "limit", "since", "garbage", "unknown_mode", "cursor_without_mode", "oversized", "zero_since"} {
		t.Run(mode, func(t *testing.T) {
			args := url.Values{"pagination": {"keyset_v1"}, "limit": {"2"}, "cursor": {first.Page.NextCursor}}
			account, app, wake := "account-1", "app-1", "wake-1"
			switch mode {
			case "app":
				app = "app-2"
			case "account":
				account = "other"
			case "wake":
				wake = "other"
			case "limit":
				args.Set("limit", "3")
			case "since":
				args.Set("since", at.Add(-time.Hour).Format(time.RFC3339))
			case "garbage":
				args.Set("cursor", "bad")
			case "unknown_mode":
				args.Set("pagination", "v2")
			case "cursor_without_mode":
				args.Del("pagination")
			case "oversized":
				args.Set("limit", "1001")
			case "zero_since":
				args.Set("since", "0001-01-01T00:00:00Z")
			}
			if _, p := ReadPage(t.Context(), store, account, app, wake, args); p == nil || p.Status != 400 {
				t.Fatal("changed page context accepted")
			}
		})
	}
	cursor := first.Page.NextCursor
	kinds := []string{first.Events[0].Kind, first.Events[1].Kind}
	for cursor != "" {
		q.Set("cursor", cursor)
		page, p := ReadPage(t.Context(), store, "account-1", "app-1", "wake-1", q)
		if p != nil || page.Page.Cursor != cursor {
			t.Fatal("page failed/context not echoed")
		}
		for _, e := range page.Events {
			kinds = append(kinds, e.Kind)
		}
		cursor = page.Page.NextCursor
	}
	if len(kinds) != 5 || kinds[2] != "wake.boot_started" || kinds[4] != "wake.proxy_first_byte" {
		t.Fatal("lost tied boundary events")
	}
	if _, p := ReadPage(t.Context(), store, "account-1", "app-1", "unknown", url.Values{"pagination": {"keyset_v1"}}); p == nil || p.Status != 404 {
		t.Fatal("unfiltered missing wake exposed")
	}
	page, p := ReadPage(t.Context(), store, "account-1", "app-1", "unknown", url.Values{"pagination": {"keyset_v1"}, "since": {"2099-01-01T00:00:00Z"}})
	if p != nil || !page.Page.Complete || len(page.Events) != 0 {
		t.Fatal("empty filtered page fabricated evidence")
	}
}

func TestCursorValidation(t *testing.T) {
	want := cursorContext("account", "app", "wake", selection{limit: 2})
	raw, err := encodeCursor(want, state.Event{ID: 9223372036854775807, At: time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "version", "id_overflow", "id_leading_zero", "zero_id", "bad_time", "fractional_time", "unknown_field", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			data, _ := base64.RawURLEncoding.DecodeString(raw[4:])
			var c map[string]any
			_ = json.Unmarshal(data, &c)
			switch mode {
			case "version":
				c["v"] = 2
			case "id_overflow":
				c["id"] = "9223372036854775808"
			case "id_leading_zero":
				c["id"] = "01"
			case "zero_id":
				c["id"] = "0"
			case "bad_time":
				c["at"] = "bad"
			case "fractional_time":
				c["at"] = "2026-10-16T10:00:00.000000001Z"
			case "unknown_field":
				c["extra"] = "x"
			}
			data, _ = json.Marshal(c)
			if mode == "trailing" {
				data = append(data, []byte(`{}`)...)
			}
			pos, e := decodeCursor("wk1."+base64.RawURLEncoding.EncodeToString(data), want)
			if (e == nil) != (mode == "valid") || mode == "valid" && strconv.FormatInt(pos.ID, 10) != "9223372036854775807" {
				t.Fatal("cursor tuple validation/precision failed")
			}
		})
	}
}
