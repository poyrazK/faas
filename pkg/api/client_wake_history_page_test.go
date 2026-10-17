// adr: 1638
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWakeHistoryPageClient(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/v1/apps/alpha/wake-timeline" || q.Get("cursor") != "native+==" || q.Get("pagination") != "keyset_v1" || q.Get("limit") != "2" {
			t.Error("page query changed")
		}
		_, _ = w.Write([]byte(`{"app":{"app_id":"app-1","slug":"alpha"},"rows":[],"page":{"mode":"keyset_v1","complete":true,"source_status":"read_succeeded"}}`))
	}))
	defer s.Close()
	out, err := NewClient(s.URL, "test").GetAppWakeHistoryPage(t.Context(), "alpha", AppWakeHistoryPageOptions{Cursor: "native+==", Limit: 2})
	if err != nil || out.Page == nil || !out.Page.Complete || out.App.AppID != "app-1" {
		t.Fatal("typed page response lost")
	}
}
