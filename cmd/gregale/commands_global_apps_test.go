package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalAppCLIUsesSeparateRegionalCredential(t *testing.T) {
	regionalKey, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPut || r.URL.Path != "/v1/global-apps/global-shop/targets/eu-fsn" || r.Header.Get("Authorization") != "Bearer directory-key" {
			t.Error("wrong directory request")
		}
		var req api.AttachGlobalAppTargetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RegionalAPIKey != regionalKey || req.RegionalAppSlug != "regional-shop" {
			t.Error("wrong proof request")
		}
		_ = json.NewEncoder(w).Encode(api.GlobalAppTargetResponse{Region: "eu-fsn", RegionalAppSlug: req.RegionalAppSlug, ResolutionStatus: "verified"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "directory-key")
	t.Setenv("GREGALE_TEST_REGIONAL_KEY", regionalKey)
	out, errOut, restore := swapIO(t)
	defer restore()
	args := []string{"attach", "global-shop", "--region", "eu-fsn", "--regional-app", "regional-shop", "--regional-token-env", "GREGALE_TEST_REGIONAL_KEY"}
	if code := cmdGlobalApp(args); code != 0 || !called {
		t.Fatalf("exit=%d called=%t", code, called)
	}
	if strings.Contains(out.String()+errOut(), regionalKey) || strings.Contains(out.String()+errOut(), "directory-key") {
		t.Fatal("credential in CLI output")
	}
	called = false
	if code := cmdGlobalApp([]string{"resolve", "global-shop"}); code == 0 || called {
		t.Fatal("implicit regional resolution accepted")
	}
	if code := cmdGlobalApp([]string{"attach", "global-shop", "--region", "eu-fsn", "--regional-app", "regional-shop", "--regional-token", regionalKey}); code == 0 || called {
		t.Fatal("plaintext command-line token accepted")
	}
}

func TestGlobalAppCLITypedDirectoryCommands(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, response string
		args                         []string
	}{
		{"create", "POST", "/v1/global-apps", `{"id":"global-id","slug":"global-shop","routing_policy":"all_selected_regions","targets":[]}`, []string{"create", "global-shop"}},
		{"show", "GET", "/v1/global-apps/global-shop", `{"id":"global-id","slug":"global-shop","routing_policy":"all_selected_regions","targets":[]}`, []string{"show", "global-shop"}},
		{"list", "GET", "/v1/global-apps", `[]`, []string{"list"}},
		{"resolve", "GET", "/v1/global-apps/global-shop/targets/eu-fsn/resolve", `{"region":"eu-fsn","api_base":"https://api.fsn.example.com","regional_app_slug":"regional-shop"}`, []string{"resolve", "global-shop", "--region", "eu-fsn"}},
		{"detach", "DELETE", "/v1/global-apps/global-shop/targets/eu-fsn", ``, []string{"detach", "global-shop", "--region", "eu-fsn"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request: %s %s", r.Method, r.URL.Path)
				}
				if tc.name == "detach" {
					w.WriteHeader(http.StatusNoContent)
				} else {
					_, _ = w.Write([]byte(tc.response))
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "directory-key")
			_, _, restore := swapIO(t)
			defer restore()
			if code := cmdGlobalApp(tc.args); code != 0 {
				t.Fatalf("exit=%d", code)
			}
		})
	}
}
