package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDevBridgeAPIToLaptopAndDurableRevocation(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.devBridgeEnabled = true
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "payments", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	relay := devbridge.NewRelay(api.DevBridgeMaxConcurrentRequests)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	relayServer := httptest.NewServer(devbridge.NewServer(relay, e.store.DevBridgeByID, nil))
	defer relayServer.Close()
	e.s.devBridgeURL = relayServer.URL
	apiServer := httptest.NewServer(e.h)
	defer apiServer.Close()
	client := api.NewClient(apiServer.URL, e.key)
	session, err := client.CreateDevBridge(ctx, api.CreateDevBridgeRequest{App: app.Slug, Environment: "development", DeveloperID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(devbridge.TokenHeader) != "" {
			t.Error("bridge token reached local service")
		}
		if r.Header.Get("Authorization") != "Bearer local-app-key" {
			t.Error("application auth lost")
		}
		if cookie, err := r.Cookie("faas_sid"); err == nil {
			t.Errorf("dashboard cookie leaked: %s", cookie.Name)
		}
		_, _ = io.WriteString(w, "local payments")
	}))
	defer local.Close()
	target, _ := url.Parse(local.URL)
	socket, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(apiServer.URL, "http")+"/v1/dev/bridges/"+session.Session.ID+"/connect", http.Header{devbridge.AccountHeader: []string{e.acct.ID}, devbridge.TokenHeader: []string{session.Credentials.AttachmentToken}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = devbridge.ServeLocal(ctx, devbridge.NewWebSocketConn(socket), target, api.DevBridgeMaxConcurrentRequests)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !relay.Connected(session.Session.ID) {
		if time.Now().After(deadline) {
			t.Fatal("connection did not attach")
		}
		time.Sleep(time.Millisecond)
	}
	request, _ := http.NewRequestWithContext(ctx, "POST", apiServer.URL+"/v1/dev/bridges/"+session.Session.ID+"/traffic/charge", nil)
	request.Header.Set(devbridge.AccountHeader, e.acct.ID)
	request.Header.Set(devbridge.TokenHeader, session.Credentials.RequestToken)
	request.Header.Set("Authorization", "Bearer local-app-key")
	request.AddCookie(&http.Cookie{Name: "faas_sid", Value: "dashboard-session"})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "local payments" {
		t.Fatalf("local response: status=%d err=%v", response.StatusCode, err)
	}
	if err := client.RevokeDevBridge(ctx, session.Session.ID); err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var problem api.Problem
	_ = json.NewDecoder(response.Body).Decode(&problem)
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("revocation not enforced: %d %+v", response.StatusCode, problem)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked connection was not closed")
	}
}
