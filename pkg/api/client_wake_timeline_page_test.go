// adr: 1637
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListWakeTimelinePageClient(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "keyset", true: "legacy"}[legacy], func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.Method != "GET" || r.URL.Path != "/v1/apps/alpha/wakes/wake-1/timeline" || q.Get("since") != "2026-10-16T10:00:00Z" || q.Get("limit") != "2" || legacy && (q.Get("pagination") != "" || q.Get("cursor") != "") || !legacy && (q.Get("pagination") != "keyset_v1" || q.Get("cursor") != "native-cursor+==") {
					t.Error("wrong native pagination request")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"app_id":"app-1","wake_id":"wake-1","limit":2,"events":[],"page":{"mode":"keyset_v1","cursor":"native-cursor+==","since":"2026-10-16T10:00:00Z","complete":true}}`))
			}))
			defer s.Close()
			c := NewClient(s.URL, "test")
			var err error
			if legacy {
				_, err = c.ListWakeTimeline(context.Background(), "alpha", "wake-1", "2026-10-16T10:00:00Z", 2)
			} else {
				_, err = c.ListWakeTimelinePage(context.Background(), "alpha", "wake-1", WakeTimelinePageOptions{Since: "2026-10-16T10:00:00Z", Cursor: "native-cursor+==", Limit: 2})
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
