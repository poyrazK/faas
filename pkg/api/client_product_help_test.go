package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductPilotHelpClientReadOnlyScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/apps/my%20app/product/help/a%2Fb%3Fc" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer owner" || r.ContentLength != 0 {
			t.Errorf("help lookup changed method/scope: %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"app_id":"test-app","request":{"id":"test-request"}}`))
	}))
	defer srv.Close()
	v, err := NewClient(srv.URL, "owner").GetProductOwnerHelp(t.Context(), "my app", "a/b?c")
	if err != nil || v.AppID != "test-app" || v.Request.ID != "test-request" {
		t.Fatalf("help lookup: %+v %v", v, err)
	}
}
