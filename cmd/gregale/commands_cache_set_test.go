package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261026145832: `cache set --method` names the method as a flag; it
// declares the same rule as `cache GET|HEAD`.
func TestCmdCacheSetDeclaresTheMethodsRule(t *testing.T) {
	var got api.CreateEdgeRuleRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/products":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-products", Slug: "products", URL: "https://products.apps.gregale.test"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/products/edge-rules":
			got = api.CreateEdgeRuleRequest{}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.EdgeRuleResponse{ID: "rule-cache-1", AppID: "app-products", Kind: "cache", Priority: 100})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"set", "/products/:id", "for", "30s", "--app", "products"}, []string{"GET"}},
		{[]string{"set", "/products/:id", "for", "30s", "--method", "head", "--app", "products"}, []string{"HEAD"}},
		{[]string{"set", "--method=HEAD", "/products/:id", "for", "30s", "--app", "products"}, []string{"HEAD"}},
	} {
		main.ResetJSONOut(t)
		if code := main.CmdCache(tc.args); code != 0 {
			t.Fatalf("cache %v = %d, want 0", tc.args, code)
		}
		if !reflect.DeepEqual(got.MatchMethods, tc.want) || got.MatchPath != "/products/*" {
			t.Fatalf("cache %v created %v %s, want %v /products/*", tc.args, got.MatchMethods, got.MatchPath, tc.want)
		}
	}
	for _, args := range [][]string{
		{"set", "/products/:id", "for", "30s", "--method", "PUT", "--app", "products"},
		{"set", "/products/:id", "for", "30s", "--method"},
	} {
		main.ResetJSONOut(t)
		if code := main.CmdCache(args); code == 0 {
			t.Errorf("cache %v = 0, want a usage error", args)
		}
	}
}
