//go:build metal

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestTCPIngressMetal drives a real guest TCP service through the production
// public edge: gatewayd-public -> tcpd -> schedd admission -> VMMD's
// ForwardTCPStream -> vmmd-tcp-bridge -> the guest network namespace.
func TestTCPIngressMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	builderBaseRef := registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "x")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideBuilderBase(t, builderBaseRef)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	// The public edge is intentionally opt-in in the harness, just like the
	// production systemd contract. Bind the test's reserved listeners locally.
	t.Setenv("FAAS_TCPD_ENABLED", "1")
	t.Setenv("FAAS_TCPD_BIND_HOST", "127.0.0.1")
	t.Setenv("FAAS_TCPD_REFRESH_INTERVAL", "50ms")
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.GatewaydPublic)
	defer h.DumpLogs(t)

	key := h.SeedAccount(context.Background(), api.PlanPro)
	slug := "tcp-metal-" + randHexSuffix()
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "app", RequireAuthn: &falsy,
		Ports: []api.WorkloadPort{{Name: "echo", Port: 5432, Protocol: api.WorkloadPortTCP}},
	}); got != 201 {
		t.Fatalf("create app %q: status=%d", slug, got)
	}

	raw, status := postMultipartDeploymentWithOverrides(t, h, key, slug, NodeFixtureTCP(t), false, &api.CreateDeploymentOverrides{Port: 5432}, "")
	if status != 202 {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)
	ctx, cancel := context.WithTimeout(context.Background(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	app, err := state.NewPgStore(pool).AppBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("load app: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, app.ID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}

	body, status := doReq(t, h, key, "POST", "/v1/apps/"+slug+"/tcp-listeners", api.CreateTCPListenerRequest{
		Name: "echo", GuestPort: 5432,
	})
	if status != 201 {
		t.Fatalf("create TCP listener: status=%d body=%s", status, body)
	}
	var listener api.TCPListenerResponse
	if err := json.Unmarshal(body, &listener); err != nil {
		t.Fatalf("decode TCP listener: %v (body=%s)", err, body)
	}
	if listener.PublicPort < state.TCPListenerPublicPortMin || listener.PublicPort > state.TCPListenerPublicPortMax {
		t.Fatalf("public TCP port=%d outside reserved range", listener.PublicPort)
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(listener.PublicPort))
	want := []byte("tcp-echo:raw bytes survive HTTP framing\n")
	got := tcpEchoWithRetry(t, addr, want, 90*time.Second)
	if !bytes.Equal(got, want) {
		t.Fatalf("TCP echo mismatch: got %q want %q", got, want)
	}
}

func tcpEchoWithRetry(t *testing.T, addr string, payload []byte, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err = conn.Write(payload); err == nil {
				if tcp, ok := conn.(*net.TCPConn); ok {
					err = tcp.CloseWrite()
				}
			}
			if err == nil {
				got := make([]byte, len("tcp-echo:")+len(payload))
				_, readErr := io.ReadFull(conn, got)
				_ = conn.Close()
				if readErr == nil {
					return got
				}
			} else {
				_ = conn.Close()
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("TCP echo did not become ready at %s", addr)
	return nil
}
