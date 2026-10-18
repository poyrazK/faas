package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgOffboardingClosesDeveloperDebugger(t *testing.T) {
	f := newOffboardingLogsFixture(t)
	if err := f.store.UpdateOrgMemberRole(t.Context(), f.org.ID, f.viewer.ID, state.OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateAccountPlan(t.Context(), f.viewer.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := f.store.CreateApp(t.Context(), state.App{AccountID: f.viewer.ID, OrgID: f.org.ID, Slug: "dev-api-0123456789ab", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60, PreviewOfSlug: "offboard-api", PreviewPrState: state.PreviewPrStateOpen})
	if err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateOrgAPIKey(t.Context(), f.org.ID, f.viewer.ID, hash, "debugger", api.ScopesAdminOnly, nil); err != nil {
		t.Fatal(err)
	}
	opened, stopped := make(chan struct{}), make(chan struct{})
	tunnel := &devDebugTunnel{auth: f.auth, log: f.log, forward: func(_ context.Context, conn net.Conn, _ gateway.Target) error {
		close(opened)
		defer close(stopped)
		_, err := conn.Read(make([]byte, 1))
		return err
	}}
	tunnel.setTargets(&fakeDebugTargets{})
	server := httptest.NewServer(devDebugRoute(tunnel, http.NotFoundHandler()))
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/apps/" + app.Slug + "/debug"
	conn, response, err := (&websocket.Dialer{HandshakeTimeout: 5 * time.Second}).Dial(url, http.Header{"Authorization": {"Bearer " + plain}})
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("debugger connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	waitOffboardingSignal(t, opened, "debugger connection")
	if err := f.store.RemoveOrgMember(t.Context(), f.org.ID, f.viewer.ID); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("revoked debugger stayed open")
	} else {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatalf("debugger was not closed by revocation: %v", err)
		}
	}
	waitOffboardingSignal(t, stopped, "upstream debugger connection cancellation")
}
