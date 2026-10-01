package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/devbridge"
)

func TestDevBridgeLocalProxyUsesScopedEnvironmentRouting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/payments/a%2Fb?currency=EUR" {
			t.Errorf("request path changed: %s", r.URL.RequestURI())
		}
		if r.Header.Get(devbridge.SessionHeader) != "session" || r.Header.Get(devbridge.AccountHeader) != "account" || r.Header.Get(devbridge.TokenHeader) != "routing-proof" {
			t.Error("scoped routing headers missing")
		}
		if r.Header.Get("Authorization") != "Bearer app-auth" {
			t.Error("application auth lost")
		}
		_, _ = io.WriteString(w, "local")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy := httptest.NewServer(bridgeLocalProxy(target, devbridge.Session{ID: "session", Scope: devbridge.Scope{AccountID: "account"}}, "routing-proof", ""))
	defer proxy.Close()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", proxy.URL+"/payments/a%2Fb?currency=EUR", nil)
	req.Header.Set("Authorization", "Bearer app-auth")
	req.Header.Set(devbridge.TokenHeader, "forged")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("status=%d", response.StatusCode)
	}
	req.Header.Set("Origin", "https://unrelated.example")
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("unrelated browser origin admitted")
	}
}

func TestDevBridgeInspectionRejectsForeignHostAndOrigin(t *testing.T) {
	inspector := devbridge.NewInspector(100, 1024)
	handler := bridgeLoopbackOnly(bridgeInspectionHandler(inspector))
	for _, test := range []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:9000", "", 200},
		{"127.0.0.1:9000", "http://127.0.0.1:9000", 200},
		{"attacker.example", "", 403},
		{"127.0.0.1:9000", "https://attacker.example", 403},
	} {
		request := httptest.NewRequest("GET", "http://127.0.0.1:9000/requests", nil)
		request.Host = test.host
		if test.origin != "" {
			request.Header.Set("Origin", test.origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("host=%s origin=%s status=%d", test.host, test.origin, response.Code)
		}
		if response.Code == 200 && response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("inspection cached")
		}
	}
}
