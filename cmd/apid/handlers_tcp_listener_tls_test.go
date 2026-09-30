package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTCPListenerTLSAPI(t *testing.T) {
	h, key, store, account := buildTestServer(t)
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tcp-tls-api", RAMMB: 256, Status: state.AppActive, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "echo", Port: 9000, Protocol: api.WorkloadPortTCP}}}})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, expected int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatalf("%s: got %d want %d: %s", method, w.Code, expected, w.Body.String())
		}
		return w
	}
	path := "/v1/apps/tcp-tls-api/tcp-listeners"
	body := `{"name":"echo","guest_port":9000,"public_port":40142,"tls":{"mode":"terminate","hostname":"Echo.Example"}}`
	request(http.MethodPost, path, body, 400)
	if _, err := store.CreateCustomDomain(ctx, "echo.example", app.ID, "token"); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, path, body, 400)
	if err := store.MarkDomainVerified(ctx, "echo.example"); err != nil {
		t.Fatal(err)
	}
	w := request(http.MethodPost, path, body, 201)
	var listener api.TCPListenerResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listener); err != nil {
		t.Fatal(err)
	}
	if listener.Enabled || listener.TLS.Mode != api.TCPListenerTLSTerminate || listener.TLS.Hostname != "echo.example" {
		t.Fatalf("listener=%+v", listener)
	}
	request(http.MethodPost, path, `{"name":"echo","guest_port":9000,"tls":{"mode":"terminate","hostname":"echo.example"}}`, http.StatusConflict)
	request(http.MethodPatch, path+"/echo", `{"enabled":true}`, 200)
	request(http.MethodPatch, path+"/echo", `{"enabled":true,"tls":{"mode":"passthrough"}}`, 400)
	w = request(http.MethodPatch, path+"/echo", `{"tls":{"mode":"passthrough"}}`, 200)
	listener = api.TCPListenerResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &listener); err != nil {
		t.Fatal(err)
	}
	if listener.Enabled || listener.TLS.Mode != api.TCPListenerTLSPassthrough || listener.TLS.Hostname != "" {
		t.Fatalf("TLS update did not atomically disable: %+v", listener)
	}
	request(http.MethodPatch, path+"/echo", `{"tls":{"mode":"terminate","hostname":"echo.example"}}`, 200)
	request(http.MethodPatch, path+"/echo", `{"enabled":true}`, 200)
	request(http.MethodGet, path, "", 200)
	request(http.MethodPatch, path+"/echo", `{"enabled":false}`, 200)
	if err := store.DeleteCustomDomain(ctx, "echo.example"); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPatch, path+"/echo", `{"enabled":true}`, 400)
}
