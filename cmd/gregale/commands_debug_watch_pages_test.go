package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 127 — amendment 2026-11-05: a watch poll pages back to the requests
// it already showed instead of dropping a burst bigger than one page, and
// reports a gap when it cannot get there.
func TestDebugWatchPollPagesThroughABurst(t *testing.T) {
	// 30 requests, newest first; pages of 4 with the cursor as an offset.
	all := make([]string, 30)
	for i := range all {
		all[i] = fmt.Sprintf("r%02d", 30-i)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		end := min(offset+limit, len(all))
		resp := api.DebugTelemetryListResponse{Since: "1h"}
		for _, id := range all[offset:end] {
			resp.Requests = append(resp.Requests, api.DebugTelemetryRequestItem{ID: id})
		}
		if end < len(all) {
			resp.NextCursor = strconv.Itoa(end)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	seenUpTo := func(newest int) map[string]string {
		seen := map[string]string{}
		for i := 1; i <= newest; i++ {
			seen[fmt.Sprintf("r%02d", i)] = "x"
		}
		return seen
	}

	t.Run("first poll reads one page", func(t *testing.T) {
		ids, missed, err := main.DebugWatchPoll(srv.URL, "my-app", 4, map[string]string{}, false)
		if err != nil || missed || !slices.Equal(ids, all[:4]) {
			t.Fatalf("ids=%v missed=%v err=%v", ids, missed, err)
		}
	})
	t.Run("burst of ten reaches the shown requests", func(t *testing.T) {
		// r01..r20 were shown; r21..r30 arrived since.
		ids, missed, err := main.DebugWatchPoll(srv.URL, "my-app", 4, seenUpTo(20), true)
		if err != nil || missed {
			t.Fatalf("missed=%v err=%v", missed, err)
		}
		for _, id := range all[:10] {
			if !slices.Contains(ids, id) {
				t.Fatalf("poll read %v, missing new request %s", ids, id)
			}
		}
	})
	t.Run("burst beyond the page bound is reported", func(t *testing.T) {
		ids, missed, err := main.DebugWatchPoll(srv.URL, "my-app", 4, seenUpTo(1), true)
		if err != nil || !missed || len(ids) != 4*main.DebugWatchMaxPages {
			t.Fatalf("ids=%d missed=%v err=%v, want %d rows and a gap", len(ids), missed, err, 4*main.DebugWatchMaxPages)
		}
	})
}
