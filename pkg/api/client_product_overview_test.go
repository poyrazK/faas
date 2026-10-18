package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductCustomerOverviewClientScope(t *testing.T) {
	if int64(ProductControlOverviewMaxResponseBytes) >= maxResponseBodyBytes {
		t.Fatal("overview response bound must fit the client's bounded JSON reader")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/my app/product/customers/overview" || r.URL.Query().Get("cursor") != "a&b" || len(r.URL.Query()) != 1 || r.Header.Get("Authorization") != "Bearer owner" {
			t.Errorf("overview request: %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"app_id":"owned-app","customers":[],"qualification_state":"unverified"}`))
	}))
	defer srv.Close()
	p, err := NewClient(srv.URL, "owner").GetProductCustomerOverview(t.Context(), "my app", "a&b")
	if err != nil || p.AppID != "owned-app" || p.Customers == nil {
		t.Fatalf("overview response: %+v %v", p, err)
	}
}
