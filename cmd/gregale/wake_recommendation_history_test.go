package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Observe audit 2026-11-21 (finding 5): the 7-day wake recommendation read the
// windowed timeline, which holds only the 100 newest instances, and still said
// "last 7d". It now walks the keyset pages and reports whether it covered the
// window.
func TestWakeRecommendationHistoryWalksPages(t *testing.T) {
	for _, tc := range []struct {
		name         string
		pages        int
		wantRows     int
		wantComplete bool
	}{
		{"covers the window", 3, 3 * api.WakeHistoryPageMax, true},
		{"stops at the page bound", wakeRecommendationMaxPages + 5, wakeRecommendationMaxPages * api.WakeHistoryPageMax, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("pagination") != "keyset_v1" {
					t.Errorf("request without keyset pagination: %s", r.URL)
				}
				page := 0
				if c := r.URL.Query().Get("cursor"); c != "" {
					page, _ = strconv.Atoi(c)
				}
				resp := api.AppWakeTimelineResponse{TriggerClassHistogram: map[string]int{"monitor": 1},
					Page: &api.AppWakeHistoryPage{Mode: "keyset_v1", Complete: page+1 >= tc.pages}}
				if !resp.Page.Complete {
					resp.Page.NextCursor = strconv.Itoa(page + 1)
				}
				for range api.WakeHistoryPageMax {
					resp.Rows = append(resp.Rows, api.WakeTimelineJSONRow{})
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()
			got, complete, err := wakeRecommendationHistory(context.Background(), api.NewClient(server.URL, "key"), "api", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Rows) != tc.wantRows || complete != tc.wantComplete {
				t.Fatalf("rows = %d complete = %v, want %d %v", len(got.Rows), complete, tc.wantRows, tc.wantComplete)
			}
			if pages := min(tc.pages, wakeRecommendationMaxPages); got.TriggerClassHistogram["monitor"] != pages {
				t.Fatalf("merged histogram = %v, want monitor=%d", got.TriggerClassHistogram, pages)
			}
		})
	}
}
