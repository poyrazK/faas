package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductCustomerActivityClientScope(t *testing.T) {
	if int64(ProductControlActivityMaxResponseBytes) >= maxResponseBodyBytes {
		t.Fatal("activity bound exceeds the client reader")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/my app/product/customers/activity" || r.URL.Query().Get("tenant") != "a&b" || r.URL.Query().Get("cursor") != "c&d" || len(r.URL.Query()) != 2 || r.Header.Get("Authorization") != "Bearer owner" {
			t.Errorf("activity request changed scope: %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"tenant_id":"a&b","events":[],"history_scope":"retained_product_control_records"}`))
	}))
	defer srv.Close()
	p, err := NewClient(srv.URL, "owner").GetProductCustomerActivity(t.Context(), "my app", "a&b", "c&d")
	if err != nil || p.TenantID != "a&b" || p.Events == nil {
		t.Fatalf("activity client: %+v %v", p, err)
	}
}
