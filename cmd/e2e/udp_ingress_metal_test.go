//go:build metal

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestUDPIngressMetal drives a real guest UDP service through the production
// public edge: gatewayd-public -> udpd -> schedd admission -> VMMD's
// ForwardUDPStream -> vmmd-udp-bridge -> the guest network namespace.
func TestUDPIngressMetal(t *testing.T) {
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
	t.Setenv("FAAS_UDPD_ENABLED", "1")
	t.Setenv("FAAS_UDPD_BIND_HOST", "127.0.0.1")
	t.Setenv("FAAS_UDPD_ALLOWED_SOURCE_CIDRS", "127.0.0.0/8")
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.GatewaydPublic)
	defer h.DumpLogs(t)

	key := h.SeedAccount(context.Background(), api.PlanPro)
	slug := "udp-metal-" + randHexSuffix()
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "app", RequireAuthn: &falsy,
		Ports: []api.WorkloadPort{{Name: "http", Port: 8080, Protocol: api.WorkloadPortTCP}, {Name: "echo", Port: 5353, Protocol: api.WorkloadPortUDP}},
	}); got != 201 {
		t.Fatalf("create app %q: status=%d", slug, got)
	}

	raw, status := postMultipartDeploymentWithOverrides(t, h, key, slug, NodeFixtureUDP(t), false, &api.CreateDeploymentOverrides{Port: 8080}, "")
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

	// Retire all eligible captures in this isolated fixture to require a cold
	// admission first. The second wake below must use a newly parked snapshot.
	if _, err := pool.Exec(ctx, `update snapshots set stale=true where deployment_id=$1`, depID); err != nil {
		t.Fatal(err)
	}
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/udp-listeners", api.CreateUDPListenerRequest{Name: "echo", GuestPort: 5353})
	if status != http.StatusCreated {
		t.Fatalf("create UDP listener: status=%d body=%s", status, body)
	}
	var listener api.UDPListenerResponse
	if err := json.Unmarshal(body, &listener); err != nil {
		t.Fatal(err)
	}
	if listener.Enabled || listener.PublicPort < api.UDPListenerPublicPortMin || listener.PublicPort > api.UDPListenerPublicPortMax {
		t.Fatalf("new UDP listener=%+v", listener)
	}
	enabled := true
	if body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug+"/udp-listeners/echo", api.UpdateUDPListenerRequest{Enabled: &enabled}); status != http.StatusOK {
		t.Fatalf("enable UDP listener: status=%d body=%s", status, body)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(listener.PublicPort))
	payload := []byte{0, 255, 128, 1, 0, 10}
	udpMetalRoundTrip(t, addr, payload, 90*time.Second)
	assertUDPMetalWakeMethod(t, ctx, pool, app.ID, "cold_boot")
	// Two live client sockets send distinct payloads before either reads a reply.
	clients := make([]*net.UDPConn, 2)
	wants := [][]byte{[]byte("peer-one"), []byte("peer-two")}
	for i := range clients {
		conn, err := net.Dial("udp4", addr)
		if err != nil {
			t.Fatal(err)
		}
		clients[i] = conn.(*net.UDPConn)
		defer clients[i].Close()
		if err := clients[i].SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := clients[i].Write(wants[i]); err != nil {
			t.Fatal(err)
		}
	}
	for i := range clients {
		buffer := make([]byte, 64)
		n, err := clients[i].Read(buffer)
		if err != nil || !bytes.Equal(buffer[:n], wants[i]) {
			t.Fatalf("peer %d: payload=%q err=%v", i, buffer[:n], err)
		}
	}
	udpMetalRoundTrip(t, addr, nil, 10*time.Second)
	udpMetalRoundTrip(t, addr, bytes.Repeat([]byte{0, 255}, api.UDPDatagramMaxBytes/2+1)[:api.UDPDatagramMaxBytes], 10*time.Second)
	enabled = false
	if body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug+"/udp-listeners/echo", api.UpdateUDPListenerRequest{Enabled: &enabled}); status != http.StatusOK {
		t.Fatalf("disable UDP listener: status=%d body=%s", status, body)
	}
	udpMetalWaitUnbound(t, addr)
	// All peers must be canceled, allowing the app to park again.
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, app.ID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("UDP peers prevented park after disable: %v", err)
	}
	for _, client := range clients {
		_ = client.Close()
	}
	enabled = true
	if body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug+"/udp-listeners/echo", api.UpdateUDPListenerRequest{Enabled: &enabled}); status != http.StatusOK {
		t.Fatalf("re-enable UDP listener: status=%d body=%s", status, body)
	}
	udpMetalRoundTrip(t, addr, []byte("after-restore"), 90*time.Second)
	assertUDPMetalWakeMethod(t, ctx, pool, app.ID, "restore")
	if body, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/"+slug+"/udp-listeners/echo", nil); status != http.StatusNoContent {
		t.Fatalf("delete UDP listener: status=%d body=%s", status, body)
	}
	udpMetalWaitUnbound(t, addr)
}

func udpMetalRoundTrip(t *testing.T, addr string, payload []byte, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := net.Dial("udp4", addr)
		if err != nil {
			t.Fatal(err)
		}
		conn := raw.(*net.UDPConn)
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if _, _, err = conn.WriteMsgUDP(payload, nil, nil); err == nil {
			buffer := make([]byte, api.UDPDatagramMaxBytes)
			n, readErr := conn.Read(buffer)
			_ = conn.Close()
			if readErr == nil {
				if !bytes.Equal(buffer[:n], payload) {
					t.Fatalf("UDP echo mismatch: got %d bytes want %d", n, len(payload))
				}
				return
			}
		} else {
			_ = conn.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("UDP echo did not become ready at %s for %d-byte payload", addr, len(payload))
}

func assertUDPMetalWakeMethod(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appID, method string) {
	t.Helper()
	instances, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if instance.State == string(state.StateRunning) && instance.WakeID != "" {
			if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, instance.WakeID, method, 10*time.Second); err != nil {
				t.Fatalf("UDP wake method must be %s: %v", method, err)
			}
			return
		}
	}
	t.Fatal("UDP response had no running instance with a durable wake identity")
}

// Acquiring the same kernel socket proves disable/delete reconciliation has
// released the endpoint; a lost UDP reply alone cannot prove that.
func udpMetalWaitUnbound(t *testing.T, addr string) {
	t.Helper()
	address, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		socket, err := net.ListenUDP("udp4", address)
		if err == nil {
			_ = socket.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("UDP endpoint %s remained bound after disable/delete", addr)
}
