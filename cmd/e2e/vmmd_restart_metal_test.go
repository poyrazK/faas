//go:build metal

// vmmd_restart_metal_test.go — vmmd restart adoption ADR acceptance on the
// full stack: apid, imaged, schedd, gatewayd and vmmd as real processes.
// A release restarts vmmd (SIGTERM, then a fresh process); the running app
// keeps its instance and its Firecracker process and keeps serving, schedd
// applies the successor's adoption report, and the successor parks and
// wakes the app afterwards.

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestVMMDRestartKeepsRunningAppMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal vmmd restart test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
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
	e2etest.OverrideVMMDConfig(t, "adoption_record = true\nadopt_running_vms = true\n")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	defer h.DumpLogs(t)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
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
		if d, derr := state.NewPgStore(pool).DeploymentByID(ctx, dep.ID); derr == nil {
			t.Logf("deployment at failure: status=%s error=%q", d.Status, d.Error)
		}
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
		t.Fatalf("first wake: status=%d body=%q", status, body)
	}
	running, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 30*time.Second)
	if err != nil {
		t.Fatalf("no running instance after wake: %v", err)
	}
	before := running[0]
	pid := firecrackerPID(t, before.ID)
	epoch := vmmdEpoch(t, pool, before.NodeID)
	if pid == 0 || epoch < 1 {
		t.Fatalf("running guest %s: firecracker pid %d, vmmd epoch %d", before.ID, pid, epoch)
	}
	// Connection-survival P1c: with bridge units on, the guest's bridge is
	// a systemd unit the successor adopts instead of replacing. A raw
	// upgrade runs as a session on it, before and after the restart.
	unitsOn := os.Getenv("FAAS_INSTANCE_BRIDGE_UNIT") == "1"
	gateway := strings.TrimPrefix(h.GatewayURL, "http://")
	var bridgeUnit string
	var bridgeRun map[string]string
	if unitsOn {
		upgradeEchoRound(t, gateway, host, "before the restart")
		units := bridgeUnits(t, before.ID)
		if len(units) != 1 {
			t.Fatalf("guest %s has bridge units %v, want one", before.ID, units)
		}
		bridgeUnit = units[0]
		bridgeRun = systemctlShow(t, bridgeUnit, "InvocationID", "MainPID", "ActiveState")
	}

	// Traffic through the whole restart. A running guest is proxied
	// directly; nothing on the request path needs vmmd.
	var ok, failed atomic.Int64
	var firstFailure atomic.Value
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
			if err := getOnce(client, url, host); err != nil {
				failed.Add(1)
				firstFailure.CompareAndSwap(nil, fmt.Sprintf("%s: %v", time.Now().UTC().Format(time.RFC3339Nano), err))
				continue
			}
			ok.Add(1)
		}
	}()
	time.Sleep(2 * time.Second)
	restartStart := time.Now()
	if err := h.PlannedRestartVMMD(30 * time.Second); err != nil {
		close(stop)
		<-done
		t.Fatalf("restart vmmd: %v", err)
	}
	restarted := time.Since(restartStart)
	deadline := time.Now().Add(60 * time.Second)
	for vmmdEpoch(t, pool, before.NodeID) <= epoch {
		if time.Now().After(deadline) {
			close(stop)
			<-done
			t.Fatalf("successor vmmd never claimed a new ownership epoch (still %d)", epoch)
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Two report intervals: schedd applies the successor's adoption report.
	time.Sleep(2 * time.Duration(api.AdoptionReportIntervalSeconds) * time.Second)
	close(stop)
	<-done
	t.Logf("vmmd restart: %s; requests during it: %d ok, %d failed (first: %v)", restarted, ok.Load(), failed.Load(), firstFailure.Load())
	if failed.Load() != 0 || ok.Load() == 0 {
		t.Fatalf("the app did not keep serving through the vmmd restart: %d ok, %d failed (first: %v)", ok.Load(), failed.Load(), firstFailure.Load())
	}
	after := instanceByID(t, pool, before.ID)
	if after.State != string(state.StateRunning) || after.Netns != before.Netns || after.GuestUID != before.GuestUID || after.HostIP != before.HostIP {
		t.Fatalf("instance after the restart = %+v, before %+v", after, before)
	}
	if got := firecrackerPID(t, before.ID); got != pid {
		t.Fatalf("firecracker pid after the restart = %d, want the adopted %d", got, pid)
	}
	if n := countInstances(t, pool, appID, "failed"); n != 0 {
		t.Fatalf("%d instance(s) failed across the restart", n)
	}
	if unitsOn {
		upgradeEchoRound(t, gateway, host, "after the restart")
		units := bridgeUnits(t, before.ID)
		run := systemctlShow(t, bridgeUnit, "InvocationID", "MainPID", "ActiveState")
		if len(units) != 1 || units[0] != bridgeUnit || run["InvocationID"] != bridgeRun["InvocationID"] ||
			run["MainPID"] != bridgeRun["MainPID"] || run["ActiveState"] != "active" {
			t.Fatalf("bridge after the restart: units %v run %v; before %s %v (the successor must adopt it)", units, run, bridgeUnit, bridgeRun)
		}
	}

	// The successor serves the platform: park, then wake again. Parking
	// also leaves no VM behind when the harness stops (adoption keeps
	// guests running through a vmmd stop).
	setAppIdleTimeout(t, h, key, "hello", api.IdleTimeoutFloorSeconds)
	if _, err := e2etest.WaitForAppParked(ctx, t, pool, appID, 90*time.Second); err != nil {
		t.Fatalf("adopted instance did not park: %v", err)
	}
	if got := firecrackerPID(t, before.ID); got != 0 {
		t.Fatalf("adopted guest still running after park: pid %d", got)
	}
	if unitsOn {
		if left := bridgeUnits(t, before.ID); len(left) != 0 {
			t.Fatalf("adopted guest parked with bridge units %v", left)
		}
	}
	// The successor finishes the adopted guest's teardown: a mount it did not
	// know about would keep the jail, and the slot's lease, behind.
	waitJailRemoved(t, before.ID, 60*time.Second)
	if body, status := doGetWithHost(t, client, url, host, 30*time.Second); status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("wake after the restart: status=%d body=%q", status, body)
	}
	woken, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 30*time.Second)
	if err != nil || woken[0].ID == before.ID {
		t.Fatalf("no fresh running instance after the restart: %v", err)
	}
	if _, err := e2etest.WaitForAppParked(ctx, t, pool, appID, 90*time.Second); err != nil {
		t.Fatalf("woken instance did not park: %v", err)
	}
	waitJailRemoved(t, woken[0].ID, 60*time.Second)
}

// upgradeEchoRound upgrades a connection through the gateway and echoes one
// message over it.
func upgradeEchoRound(t *testing.T, gateway, host, msg string) {
	t.Helper()
	conn, br := upgradeEchoConn(t, gateway, host)
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, msg+"\n"); err != nil {
		t.Fatalf("upgrade echo write: %v", err)
	}
	if got, err := br.ReadString('\n'); err != nil || got != msg+"\n" {
		t.Fatalf("upgrade echo of %q = %q, %v", msg, got, err)
	}
}

func getOnce(client *http.Client, url, host string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Host = host
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(body.String()) != helloBody {
		return fmt.Errorf("status %d body %q", resp.StatusCode, body.String())
	}
	return nil
}

// firecrackerPID returns the pid of the Firecracker process serving
// instance, or 0.
func firecrackerPID(t *testing.T, instance string) int {
	t.Helper()
	dirs, err := filepath.Glob("/proc/[0-9]*/cmdline")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range dirs {
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(raw, []byte("\x00--id\x00"+instance+"\x00")) || !bytes.Contains(raw, []byte("firecracker")) {
			continue
		}
		if pid, err := strconv.Atoi(filepath.Base(filepath.Dir(path))); err == nil {
			return pid
		}
	}
	return 0
}

// waitJailRemoved waits for every jailer chroot of instance to be removed.
func waitJailRemoved(t *testing.T, instance string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		jails, err := filepath.Glob(filepath.Join("/srv/fc/jail", "*", instance))
		if err != nil {
			t.Fatal(err)
		}
		if len(jails) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("jail of %s not removed after teardown: %v", instance, jails)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func vmmdEpoch(t *testing.T, pool *pgxpool.Pool, node string) int64 {
	t.Helper()
	var epoch int64
	if err := pool.QueryRow(context.Background(), `SELECT vmmd_epoch FROM compute_nodes WHERE id = $1`, node).Scan(&epoch); err != nil {
		t.Fatalf("read vmmd epoch: %v", err)
	}
	return epoch
}

func instanceByID(t *testing.T, pool *pgxpool.Pool, id string) state.Instance {
	t.Helper()
	ins, err := state.NewPgStore(pool).InstanceByID(context.Background(), id)
	if err != nil {
		t.Fatalf("read instance %s: %v", id, err)
	}
	return ins
}

func countInstances(t *testing.T, pool *pgxpool.Pool, appID, st string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM instances WHERE app_id = $1 AND state = $2`, appID, st).Scan(&n); err != nil {
		t.Fatalf("count %s instances: %v", st, err)
	}
	return n
}
