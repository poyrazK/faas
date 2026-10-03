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
		if r.URL.Path == "/echo" {
			_ = http.NewResponseController(w).EnableFullDuplex()
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			_, _ = io.Copy(w, r.Body)
			return
		}
		_, _ = io.WriteString(w, "local payments")
	}))
	defer local.Close()
	target, _ := url.Parse(local.URL)
	socket, handshake, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(apiServer.URL, "http")+"/v1/dev/bridges/"+session.Session.ID+"/connect", http.Header{devbridge.AccountHeader: []string{e.acct.ID}, devbridge.TokenHeader: []string{session.Credentials.AttachmentToken}})
	if handshake != nil && handshake.Body != nil {
		_ = handshake.Body.Close()
	}
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
	// adr: 379 — prove account control reads observe the actual proxied
	// laptop upgrade and request, without needing an attachment credential.
	// The response can reach the client before the reverse proxy closes its
	// upstream body and records completion. Wait for that server-side event.
	activityCtx, activityCancel := context.WithTimeout(ctx, 5*time.Second)
	defer activityCancel()
	activity, err := client.GetDevBridgeActivity(activityCtx, session.Session.ID)
	for err == nil && len(activity.Requests) == 1 && !activity.Requests[0].Complete {
		select {
		case <-activityCtx.Done():
			t.Fatalf("activity completion timed out: %+v err=%v", activity, activityCtx.Err())
		case <-time.After(time.Millisecond):
		}
		activity, err = client.GetDevBridgeActivity(activityCtx, session.Session.ID)
	}
	if err != nil || activity.ConnectionState != "connected" || len(activity.Requests) != 1 || !activity.Requests[0].Complete || activity.Requests[0].Path != "/charge" {
		t.Fatalf("activity did not observe live traffic: %+v err=%v", activity, err)
	}
	inventory, err := client.ListDevBridges(ctx)
	if err != nil || len(inventory.Sessions) != 1 || inventory.Sessions[0].ConnectionState != "connected" {
		t.Fatalf("inventory did not observe connection: %+v err=%v", inventory, err)
	}
	// Response headers must arrive before the upload ends across both HTTP/1
	// proxy hops, the WebSocket/HTTP2 tunnel and the local HTTP/1 process.
	streamCtx, streamCancel := context.WithTimeout(ctx, 5*time.Second)
	defer streamCancel()
	reader, writer := io.Pipe()
	go func() { <-streamCtx.Done(); _ = writer.CloseWithError(streamCtx.Err()) }()
	defer func() { _ = writer.Close() }()
	stream := request.Clone(streamCtx)
	stream.URL.Path = "/v1/dev/bridges/" + session.Session.ID + "/traffic/echo"
	stream.Body, stream.ContentLength = reader, -1
	response, err = http.DefaultClient.Do(stream)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("streamed", 10000)
	go func() { _, _ = io.WriteString(writer, payload); _ = writer.Close() }()
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != payload {
		t.Fatalf("duplex body len=%d err=%v", len(body), err)
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
