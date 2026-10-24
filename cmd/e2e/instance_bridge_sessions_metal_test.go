//go:build metal

// instance_bridge_sessions_metal_test.go — connection-survival ADR P1b
// acceptance on the full stack (apid, imaged, schedd, gatewayd-internal,
// gatewayd-public, tcpd, udpd and vmmd as real processes). An app deployed
// from an image (no builder) carries all three session kinds: a raw upgrade
// through the gateway (ForwardRawStream), a TCP ingress listener
// (ForwardTCPStream) and a UDP ingress listener (ForwardUDPStream). With
// FAAS_INSTANCE_BRIDGE_SESSIONS=1 in the environment they run as sessions
// on the instance's bridge; without it, as per-connection bridge processes.
// The acceptance driver runs it both ways, and in the session pass replaces
// the per-connection bridge binaries with ones that fail.

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInstanceBridgeSessionsMetal(t *testing.T) {
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
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")
	t.Setenv("FAAS_TCPD_ENABLED", "1")
	t.Setenv("FAAS_TCPD_BIND_HOST", "127.0.0.1")
	t.Setenv("FAAS_TCPD_REFRESH_INTERVAL", "50ms")
	t.Setenv("FAAS_UDPD_ENABLED", "1")
	t.Setenv("FAAS_UDPD_BIND_HOST", "127.0.0.1")
	t.Setenv("FAAS_UDPD_ALLOWED_SOURCE_CIDRS", "127.0.0.0/8")

	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.GatewaydPublic, "FAAS_TCPD_TLS_CERT_DIR="+t.TempDir())
	defer h.DumpLogs(t)
	key := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.HelloImageAboveBaseWithArgs("library/hello", helloBody, "-tcp-echo", "5432", "-udp-echo", "5353")
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: "hello", Type: "app", RequireAuthn: &falsy,
		Ports: []api.WorkloadPort{
			{Name: "http", Port: 8080, Protocol: api.WorkloadPortTCP},
			{Name: "echo", Port: 5432, Protocol: api.WorkloadPortTCP},
			{Name: "echo-udp", Port: 5353, Protocol: api.WorkloadPortUDP},
		},
	}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	// Pro apps default to public_auth_mode=bearer as well as require_authn;
	// the requests below are anonymous, so open both gates.
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/hello", api.UpdateAppRequest{RequireAuthn: &falsy,
		PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}}); status != http.StatusOK {
		t.Fatalf("open app auth: status %d", status)
	}
	appID := mustGetAppID(t, h, key, "hello")
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, raw)
	}
	ctx := context.Background()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 15*time.Minute); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 5*time.Minute); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}
	url, host := gatewayAppURL(h, "hello"), "hello.apps.test.example"
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(ctx, t, client, url, 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	if body, status := doGetWithHost(t, client, url, host, 30*time.Second); status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("wake: status=%d body=%q", status, body)
	}

	t.Run("raw upgrade", func(t *testing.T) {
		// Three upgraded connections open at once, then interleaved
		// messages: concurrent sessions on one bridge stay apart.
		gateway := strings.TrimPrefix(h.GatewayURL, "http://")
		conns := make([]*bufio.ReadWriter, 3)
		for i := range conns {
			conn, br := upgradeEchoConn(t, gateway, host)
			conns[i] = bufio.NewReadWriter(br, bufio.NewWriter(conn))
		}
		for round := 0; round < 3; round++ {
			for i, rw := range conns {
				msg := fmt.Sprintf("conn %d round %d\n", i, round)
				if _, err := rw.WriteString(msg); err != nil {
					t.Fatalf("write %q: %v", msg, err)
				}
				if err := rw.Flush(); err != nil {
					t.Fatalf("flush %q: %v", msg, err)
				}
				got, err := rw.ReadString('\n')
				if err != nil || got != msg {
					t.Fatalf("echo of %q = %q, %v", msg, got, err)
				}
			}
		}
	})

	t.Run("TCP ingress", func(t *testing.T) {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/tcp-listeners", api.CreateTCPListenerRequest{Name: "echo", GuestPort: 5432})
		if status != http.StatusCreated {
			t.Fatalf("create TCP listener: status=%d body=%s", status, body)
		}
		var listener api.TCPListenerResponse
		if err := json.Unmarshal(body, &listener); err != nil {
			t.Fatalf("decode TCP listener: %v (body=%s)", err, body)
		}
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(listener.PublicPort))
		for i := 0; i < 3; i++ {
			payload := []byte(fmt.Sprintf("tcp session %d\n", i))
			want := append([]byte("tcp-echo:"), payload...)
			if got := tcpEchoWithRetry(t, addr, payload, 90*time.Second); !bytes.Equal(got, want) {
				t.Fatalf("TCP echo %d: got %q want %q", i, got, want)
			}
		}
	})

	t.Run("UDP ingress", func(t *testing.T) {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/hello/udp-listeners", api.CreateUDPListenerRequest{Name: "echo-udp", GuestPort: 5353})
		if status != http.StatusCreated {
			t.Fatalf("create UDP listener: status=%d body=%s", status, body)
		}
		var listener api.UDPListenerResponse
		if err := json.Unmarshal(body, &listener); err != nil {
			t.Fatal(err)
		}
		enabled := true
		if body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/hello/udp-listeners/echo-udp", api.UpdateUDPListenerRequest{Enabled: &enabled}); status != http.StatusOK {
			t.Fatalf("enable UDP listener: status=%d body=%s", status, body)
		}
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(listener.PublicPort))
		udpMetalRoundTrip(t, addr, []byte{0, 255, 128, 1, 0, 10}, 90*time.Second)
		udpMetalRoundTrip(t, addr, bytes.Repeat([]byte{0, 255}, api.UDPDatagramMaxBytes/2+1)[:api.UDPDatagramMaxBytes], 10*time.Second)
	})
}

// upgradeEchoConn opens a connection through the gateway and upgrades it on
// the fixture's /upgrade-echo; reads continue from the returned reader.
func upgradeEchoConn(t *testing.T, gateway, host string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", gateway, 5*time.Second)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET /upgrade-echo HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: faas-p1b-test\r\n\r\n", host); err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("upgrade: status=%d body=%q", resp.StatusCode, body)
	}
	return conn, br
}
