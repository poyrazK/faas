// adr: 20261017233500
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGetSelectedAppTraceNeverUsesAccountLookup(t *testing.T) {
	ids := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	calls := 0
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/account/traces/4bf92f3577b34da6a3ce929d0e0e4736/selected-apps" || !reflect.DeepEqual(r.URL.Query()["app_id"], ids) || r.URL.Query().Get("limit") != "7" {
			t.Error("scoped native selection changed")
		}
		w.WriteHeader(404)
	}))
	defer b.Close()
	_, err := NewClient(b.URL, "fixture").GetSelectedAppTrace(context.Background(), "4bf92f3577b34da6a3ce929d0e0e4736", ids, 7)
	if err == nil || calls != 1 {
		t.Fatal("older API failure triggered a broader fallback")
	}
}
