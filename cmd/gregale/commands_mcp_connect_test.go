package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPCatalogBrowserConnection(t *testing.T) {
	nonce := strings.Repeat("A", 43)
	calls := 0
	redirect := ""
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req api.MCPCatalogOAuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Role != "reader" {
			t.Fatal("role binding lost")
		}
		if req.Action == "begin" {
			redirect = req.RedirectURI
			u, _ := url.Parse(redirect)
			if u.Hostname() != "127.0.0.1" || u.Path != "/callback" {
				t.Fatal("callback is not loopback")
			}
			_ = json.NewEncoder(w).Encode(api.MCPCatalogOAuthResponse{Revision: "begun", State: "authorization_pending", AuthorizationURL: "https://provider.example/authorize?state=" + nonce, CallbackState: nonce})
			return
		}
		if req.Action != "complete" || req.Revision != "begun" || req.Code != "PRIVATE_CODE" || req.State != nonce {
			t.Fatal("callback lost server binding")
		}
		_ = json.NewEncoder(w).Encode(api.MCPCatalogOAuthResponse{Revision: "connected", State: "connected"})
	}))
	defer platform.Close()
	t.Setenv("FAAS_API", platform.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_guided")
	client, err := main.AuthedClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	out, err := main.RunMCPBrowserConnection(ctx, client, "guided-app", api.MCPCatalogOAuthRequest{Role: "reader", Revision: "initial"}, func(target string) error {
		if !strings.HasPrefix(target, "https://provider.example/") {
			t.Fatal("wrong browser target")
		}
		for _, state := range []string{"wrong", nonce} {
			response, e := http.Get(redirect + "?code=PRIVATE_CODE&state=" + state)
			if e != nil {
				t.Fatal(e)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if state == "wrong" && response.StatusCode != 400 {
				t.Fatal("wrong state accepted")
			}
			if state == nonce && response.StatusCode != 200 {
				t.Fatal("valid callback refused")
			}
		}
		return nil
	})
	if err != nil || out.State != "connected" || calls != 2 {
		t.Fatal("browser connection failed", err)
	}
	check := http.Client{Timeout: 100 * time.Millisecond}
	if response, e := check.Get(redirect); e == nil {
		response.Body.Close()
		t.Fatal("loopback listener leaked")
	}
}

func TestMCPCatalogFixedCallbackPortConflict(t *testing.T) {
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	t.Setenv("FAAS_API", "http://127.0.0.1:"+strconv.Itoa(port))
	t.Setenv("FAAS_TOKEN", "fp_test_fixed")
	client, e := main.AuthedClient()
	if e != nil {
		t.Fatal(e)
	}
	launched := false
	if _, e = main.RunMCPBrowserConnection(t.Context(), client, "fixed-app", api.MCPCatalogOAuthRequest{}, func(string) error { launched = true; return nil }, port); e == nil || launched {
		t.Fatal("occupied fixed callback port did not fail before browser launch")
	}
}

func TestMCPCatalogProviderCheckCLI(t *testing.T) {
	revision := "11111111-1111-4111-8111-111111111111"
	calls := 0
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: revision, Role: "reader", Observer: &api.MCPCatalogObserverConfig{OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, ClientID: "reader", TokenEndpoint: "https://provider.example/token", AuthorizationEndpoint: "https://provider.example/authorize", Issuer: "https://provider.example", DiscoveryURL: "https://provider.example/metadata"}}}})
			return
		}
		var req api.MCPCatalogOAuthRequest
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Fatal(e)
		}
		if req.Action != "check" || req.Revision != revision || req.Role != "reader" {
			t.Fatal("provider check lost role/revision binding")
		}
		_ = json.NewEncoder(w).Encode(api.MCPCatalogOAuthResponse{Revision: revision, State: "unsupported", Checks: []api.MCPCatalogOAuthCheck{{Name: "pkce", State: "failed", Reason: "required_capability_not_supported"}}})
	}))
	defer platform.Close()
	t.Setenv("FAAS_API", platform.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_provider")
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	var output strings.Builder
	(*main.OsStdout) = &output
	(*main.JsonOutput) = false
	t.Cleanup(func() { (*main.OsStdout) = oldOut; (*main.JsonOutput) = oldJSON })
	if code := main.CmdMCPCatalogMonitor([]string{"provider-check", "--app", "provider-app", "--role", "reader"}); code != 1 || calls != 2 || !strings.Contains(output.String(), "pkce: failed") {
		t.Fatalf("provider check result: %d requests=%d", code, calls)
	}
}
func TestMCPCatalogBrowserRejectsIncompatibleProvider(t *testing.T) {
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.MCPCatalogOAuthResponse{Revision: "reviewed", State: "unsupported"})
	}))
	defer platform.Close()
	t.Setenv("FAAS_API", platform.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_unsupported")
	client, e := main.AuthedClient()
	if e != nil {
		t.Fatal(e)
	}
	launched := false
	_, e = main.RunMCPBrowserConnection(t.Context(), client, "provider-app", api.MCPCatalogOAuthRequest{Role: "reader"}, func(string) error { launched = true; return nil })
	if e == nil || launched || !strings.Contains(main.SafeMCPConnectionError(e).Error(), "provider-check") {
		t.Fatal("incompatible provider opened browser or lacked diagnosis")
	}
}
