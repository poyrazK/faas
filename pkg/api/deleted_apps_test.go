package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDeletedAppsPreservesCursorAndExactIdentity(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test" {
			t.Errorf("unexpected read: %s, auth=%q", r.Method, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/deleted-apps":
			if r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("cursor") != "opaque+/=" {
				t.Errorf("pagination query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"account_id":"account-a","apps":[{"account_id":"account-a","id":"` + id + `","slug":"retained","deleted_at":null,"delete_grace_until":null,"restore_eligible":false,"restore_blocker":"metadata_unavailable","evaluated_at":"2026-10-12T12:00:00Z"}],"next_cursor":"next","evaluated_at":"2026-10-12T12:00:00Z"}`))
		case "/v1/deleted-apps/" + id:
			_, _ = w.Write([]byte(`{"account_id":"account-a","id":"` + id + `","slug":"retained","deleted_at":null,"delete_grace_until":null,"restore_eligible":false,"restore_blocker":"metadata_unavailable","evaluated_at":"2026-10-12T12:00:00Z"}`))
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "test")
	page, err := client.ListDeletedApps(context.Background(), "opaque+/=", 25)
	if err != nil || page.AccountID != "account-a" || len(page.Apps) != 1 || page.Apps[0].ID != id || page.NextCursor != "next" {
		t.Fatalf("list = %+v, %v", page, err)
	}
	detail, err := client.GetDeletedApp(context.Background(), id)
	if err != nil || detail.AccountID != "account-a" || detail.ID != id || detail.DeletedAt != nil || detail.RestoreEligible || detail.RestoreBlocker != "metadata_unavailable" {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
}

func TestClientRestoreDeletedAppDoesNotReplayOrFallback(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/deleted-apps/"+id+"/restore" {
			t.Errorf("unexpected restore route: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("non-replay-safe restore advertised transport replay safety")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"capacity","title":"Response uncertain","status":503}`))
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "test").RestoreDeletedApp(context.Background(), id)
	if err == nil || calls != 1 {
		t.Fatalf("restore = %v, calls=%d", err, calls)
	}
}
