//go:build metal

package e2e_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	certificateDirectory := t.TempDir()
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.GatewaydPublic, "FAAS_TCPD_TLS_CERT_DIR="+certificateDirectory)
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
	payload := []byte("raw bytes survive HTTP framing\n")
	want := append([]byte("tcp-echo:"), payload...)
	got := tcpEchoWithRetry(t, addr, payload, 90*time.Second)
	if !bytes.Equal(got, want) {
		t.Fatalf("TCP echo mismatch: got %q want %q", got, want)
	}
	store := state.NewPgStore(pool)
	beforeTLS, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	priorWakeIDs := make(map[string]bool, len(beforeTLS))
	for _, instance := range beforeTLS {
		priorWakeIDs[instance.WakeID] = true
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, app.ID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("no park before TLS wake: %v", err)
	}
	hostname := slug + ".example"
	// Fixture ownership is seeded directly; DNS/TXT verification is covered by
	// the domain acceptance suite rather than requiring external DNS here.
	if _, err := store.CreateCustomDomain(ctx, hostname, app.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, hostname); err != nil {
		t.Fatal(err)
	}
	_, certificate := tcpMetalTLSBundle(t, certificateDirectory, hostname, time.Now().Add(time.Hour))
	policy := api.TCPListenerTLSConfig{Mode: api.TCPListenerTLSTerminate, Hostname: hostname}
	body, status = doReq(t, h, key, "PATCH", "/v1/apps/"+slug+"/tcp-listeners/echo", api.UpdateTCPListenerRequest{TLS: &policy})
	if status != 200 {
		t.Fatalf("configure TLS: status=%d body=%s", status, body)
	}
	var updated api.TCPListenerResponse
	if err := json.Unmarshal(body, &updated); err != nil || updated.Enabled {
		t.Fatalf("TLS update must disable: response=%+v err=%v", updated, err)
	}
	tcpMetalWaitUnbound(t, addr)
	enabled := true
	body, status = doReq(t, h, key, "PATCH", "/v1/apps/"+slug+"/tcp-listeners/echo", api.UpdateTCPListenerRequest{Enabled: &enabled})
	if status != 200 {
		t.Fatalf("enable TLS: status=%d body=%s", status, body)
	}
	tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_tls_listeners_ready", 1)
	leaf, err := x509.ParseCertificate(certificate)
	if err != nil {
		t.Fatal(err)
	}
	tcpMetalWaitTLSStatus(t, ctx, h, key, slug, hostname, "ready", true, leaf.NotAfter)
	wrongSNI, dialErr := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, &tls.Config{ServerName: "wrong.example", InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if dialErr == nil {
		_ = wrongSNI.Close()
		t.Fatal("native edge accepted wrong TLS server name")
	}
	// A failed client handshake alone could mean an unbound port. Require the
	// production edge's rejection and completion observations before checking
	// that no new durable instance wake identity appeared.
	tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_sessions_rejected_total{reason=\"tls_handshake\"}", 1)
	tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_active_sessions", 0)
	afterReject, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range afterReject {
		if !priorWakeIDs[instance.WakeID] || state.State(instance.State) == state.StateRunning {
			t.Fatalf("wrong SNI woke a workload: instance=%s wake=%s state=%s", instance.ID, instance.WakeID, instance.State)
		}
	}
	for index, failure := range []string{"expired", "missing"} {
		if failure == "expired" {
			tcpMetalTLSBundle(t, certificateDirectory, hostname, time.Now().Add(-time.Minute))
		} else if err := os.Remove(filepath.Join(certificateDirectory, hostname+".pem")); err != nil {
			t.Fatal(err)
		}
		tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_tls_listeners_ready", 0)
		tcpMetalWaitTLSStatus(t, ctx, h, key, slug, hostname, "not_ready", true, time.Time{})
		// Disable client verification so rejection proves the serving edge
		// refused its own unavailable certificate before workload admission.
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, &tls.Config{ServerName: hostname, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
		if err == nil {
			_ = conn.Close()
			t.Fatalf("native edge accepted %s certificate", failure)
		}
		tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_sessions_rejected_total{reason=\"tls_handshake\"}", float64(index+2))
		tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_active_sessions", 0)
		instances, err := store.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, instance := range instances {
			if !priorWakeIDs[instance.WakeID] || state.State(instance.State) == state.StateRunning {
				t.Fatalf("%s certificate woke a workload: instance=%s wake=%s state=%s", failure, instance.ID, instance.WakeID, instance.State)
			}
		}
	}
	roots, certificate := tcpMetalTLSBundle(t, certificateDirectory, hostname, time.Now().Add(time.Hour))
	leaf, err = x509.ParseCertificate(certificate)
	if err != nil {
		t.Fatal(err)
	}
	tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_tls_listeners_ready", 1)
	tcpMetalWaitTLSStatus(t, ctx, h, key, slug, hostname, "ready", true, leaf.NotAfter)
	deadline := time.Now().Add(90 * time.Second)
	var tlsReply []byte
	for time.Now().Before(deadline) {
		conn, dialErr := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, &tls.Config{ServerName: hostname, RootCAs: roots, MinVersion: tls.VersionTLS12})
		if dialErr == nil {
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if !bytes.Equal(conn.ConnectionState().PeerCertificates[0].Raw, certificate) {
				_ = conn.Close()
				t.Fatal("native edge presented unexpected TLS certificate")
			}
			if _, writeErr := conn.Write(payload); writeErr == nil {
				tlsReply = make([]byte, len(want))
				_, readErr := io.ReadFull(conn, tlsReply)
				_ = conn.Close()
				if readErr == nil {
					break
				}
			} else {
				_ = conn.Close()
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !bytes.Equal(tlsReply, want) {
		t.Fatalf("native TLS wake/echo mismatch: got=%q want=%q", tlsReply, want)
	}
	instances, err := e2etest.WaitForInstanceState(ctx, t, pool, app.ID, state.StateRunning, 30*time.Second)
	if err != nil {
		t.Fatalf("TLS did not wake real instance: %v", err)
	}
	wakeID := ""
	for _, instance := range instances {
		if state.State(instance.State) == state.StateRunning {
			wakeID = instance.WakeID
			break
		}
	}
	if wakeID == "" {
		t.Fatal("TLS running instance has no wake identity")
	}
	if priorWakeIDs[wakeID] {
		t.Fatal("TLS acceptance reused an earlier wake instead of waking after park")
	}
	if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, "restore", 10*time.Second); err != nil {
		t.Fatalf("TLS acceptance requires durable restore evidence: %v", err)
	}
	// Keep a real guest stream open across disable. Assert an actual terminal
	// read, then acquire the kernel listener to prove reconciliation released it.
	live, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, &tls.Config{ServerName: hostname, RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	tcpMetalTLSEcho(t, live, payload, want)
	rotatedRoots, rotatedCertificate := tcpMetalTLSBundle(t, certificateDirectory, hostname, time.Now().Add(2*time.Hour))
	rotated, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, &tls.Config{ServerName: hostname, RootCAs: rotatedRoots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("trusted handshake after rotation: %v", err)
	}
	defer rotated.Close()
	if !bytes.Equal(rotated.ConnectionState().PeerCertificates[0].Raw, rotatedCertificate) {
		t.Fatal("new native handshake retained the old certificate")
	}
	tcpMetalTLSEcho(t, rotated, payload, want)
	tcpMetalTLSEcho(t, live, payload, want)
	if !bytes.Equal(live.ConnectionState().PeerCertificates[0].Raw, certificate) {
		t.Fatal("rotation changed an established TLS session's certificate")
	}
	rotatedLeaf, err := x509.ParseCertificate(rotatedCertificate)
	if err != nil {
		t.Fatal(err)
	}
	tcpMetalWaitTLSStatus(t, ctx, h, key, slug, hostname, "ready", true, rotatedLeaf.NotAfter)
	enabled = false
	body, status = doReq(t, h, key, "PATCH", "/v1/apps/"+slug+"/tcp-listeners/echo", api.UpdateTCPListenerRequest{Enabled: &enabled})
	if status != http.StatusOK {
		t.Fatalf("disable TLS: status=%d body=%s", status, body)
	}
	for _, connection := range []*tls.Conn{live, rotated} {
		_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, err = connection.Read(make([]byte, 1))
		if err == nil {
			t.Fatal("disabled TLS stream remained readable")
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatalf("disabled TLS stream was not closed: %v", err)
		}
	}
	tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_active_sessions", 0)
	tcpMetalWaitMetric(t, ctx, h.GatewayPublicControlURL, "gatewayd_public_tcp_tls_listeners_ready", 0)
	tcpMetalWaitTLSStatus(t, ctx, h, key, slug, hostname, "unknown", false, time.Time{})
	tcpMetalWaitUnbound(t, addr)
}

func tcpMetalTLSEcho(t *testing.T, connection *tls.Conn, payload, want []byte) {
	t.Helper()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len(want))
	if _, err := io.ReadFull(connection, reply); err != nil || !bytes.Equal(reply, want) {
		t.Fatalf("native TLS guest reply=%q want=%q err=%v", reply, want, err)
	}
}

func tcpMetalWaitTLSStatus(t *testing.T, ctx context.Context, h *e2etest.Harness, key, slug, hostname, want string, enabled bool, expiry time.Time) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	var response api.TCPListenerTLSStatusResponse
	for time.Now().Before(deadline) && ctx.Err() == nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.APIDURL+"/v1/apps/"+slug+"/tcp-listeners/echo/tls-status", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+key)
		result, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(result.Body, 1<<20))
		_ = result.Body.Close()
		if err != nil || result.StatusCode != http.StatusOK {
			t.Fatalf("customer TLS status: HTTP %d err=%v body=%s", result.StatusCode, err, body)
		}
		response = api.TCPListenerTLSStatusResponse{}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.Scope != "observed_edges" || response.Name != "echo" || response.TLS.Hostname != hostname || response.TLS.Mode != api.TCPListenerTLSTerminate || response.Enabled != enabled || len(response.Observations) != 1 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		observation := response.Observations[0]
		if observation.EdgeID == "" || observation.Status != want || observation.ObservedAt.IsZero() || observation.ObservedAt.After(time.Now()) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if (expiry.IsZero() && observation.NotAfter == nil) || (!expiry.IsZero() && observation.NotAfter != nil && observation.NotAfter.Equal(expiry)) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("native customer certificate status did not become %s: %+v", want, response)
}

func tcpMetalWaitUnbound(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		socket, err := net.Listen("tcp", addr)
		if err == nil {
			_ = socket.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("disabled TCP endpoint %s remained bound", addr)
}

func tcpMetalWaitMetric(t *testing.T, ctx context.Context, controlURL, series string, want float64) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, controlURL+"/metrics", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				for _, line := range strings.Split(string(body), "\n") {
					fields := strings.Fields(line)
					if len(fields) == 2 && fields[0] == series {
						value, parseErr := strconv.ParseFloat(fields[1], 64)
						if parseErr == nil && value == want {
							return
						}
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("native public edge metric %s did not become %g: context=%v", series, want, ctx.Err())
}

func tcpMetalTLSBundle(t *testing.T, directory, hostname string, expiry time.Time) (*x509.CertPool, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{hostname}, NotBefore: time.Now().Add(-time.Hour), NotAfter: expiry, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, identity, identity, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	bundle := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})...)
	staged := filepath.Join(directory, hostname+".staged")
	if err := os.WriteFile(staged, bundle, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, filepath.Join(directory, hostname+".pem")); err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return roots, der
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
