//go:build metal && function_background_native

package e2e_test

// adr: 1573
// adr: 1576

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/functionsql"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestFunctionResourcesPostgresMetal owns independent SQL and checkpoint
// witnesses. Zero sessions is required while capture is still blocked, so
// killing the VM after capture cannot masquerade as pool drainage.
func TestFunctionResourcesPostgresMetal(t *testing.T) {
	requireFunctionBackgroundNative(t)
	for _, runtime := range []string{"node22", "node24"} {
		t.Run(runtime, func(t *testing.T) { runFunctionResourcesPostgresMetal(t, runtime, "") })
	}
}

func runFunctionResourcesPostgresMetal(t *testing.T, runtime, migrationScenario string) {
	runner := os.Getenv("FAAS_E2E_FUNCTION_RUNNER_" + strings.ToUpper(runtime))
	if info, err := os.Stat(runner); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatal("requires the current qualified runner for " + runtime)
	}
	ip, err := netip.ParseAddr(os.Getenv("GREGALE_POSTGRES_NATIVE_SQL_IP"))
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatal("requires the prepared isolated SQL acceptance interface")
	}
	ca, err := os.ReadFile(os.Getenv("GREGALE_POSTGRES_NATIVE_CA_PATH"))
	if err != nil || len(ca) == 0 {
		t.Fatal("requires the public native SQL CA with the proxy IP in its SAN")
	}
	catalog := pgtest.OpenMigrated(t)
	if catalog == nil {
		t.Fatal("native SQL gate requires its disposable catalog")
	}
	if err := dbMigrateUp(t, catalog); err != nil {
		t.Fatal(err)
	}
	sql := functionsql.New(t)
	proxy := postgresfixture.OpenProxy(t, sql.Admin, ip.String(), 5432)
	control := functionsql.NewControl(t, ip.String())
	defer control.Release()
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	h := e2etest.Start(t, catalog, e2etest.All, append(secretSealingEnv(t), "FAAS_FUNCTION_RUNNER_"+strings.ToUpper(runtime)+"="+runner)...)
	key := h.SeedAccount(t.Context(), api.PlanPro)
	slug := "pool-" + randHexSuffix()
	public := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "function", Runtime: runtime, RequireAuthn: &public, FunctionConcurrency: 1}); status != http.StatusCreated {
		t.Fatal("create native SQL function")
	}
	appID := mustGetAppID(t, h, key, slug)
	store := state.NewPgStore(catalog)
	t.Cleanup(func() {
		control.Release()
		if t.Failed() {
			h.DumpLogs(t)
		}
		if _, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/"+slug, nil); status >= 300 {
			t.Error("retire native SQL function")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		backgroundNativeWait(t, ctx, func() bool {
			instances, err := store.ListInstancesForApp(ctx, appID)
			if err != nil {
				return false
			}
			for _, ins := range instances {
				if state.State(ins.State).CountsForRAM() {
					return false
				}
			}
			return true
		})
	})
	ports := []int{5432, int(control.Port)}
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{EgressPorts: &ports}); status != http.StatusOK {
		t.Fatal("configure isolated SQL/control egress")
	}
	for name, value := range map[string]string{
		"DATABASE_URL": sql.ConnectionString(proxy.Host, proxy.Port), "NODE_EXTRA_CA_CERTS": "/app/sql-ca.pem",
		"FUNCTION_SQL_CONTROL_URL": control.URL, "FUNCTION_SQL_CONTROL_TOKEN": control.Token,
	} {
		if _, status := doReq(t, h, key, http.MethodPut, "/v1/apps/"+slug+"/secrets/"+name, api.PutAppSecretRequest{Value: value}); status != http.StatusOK {
			t.Fatal("seal native SQL fixture inputs")
		}
	}
	if migrationScenario != "" {
		enrollNativeSQLMigration(t, store, appID, ip.String())
	}
	files := functionsql.Files(t)
	files["sql-ca.pem"] = string(ca)
	body, status := postBinaryFunctionDeployment(t, h, key, slug, buildTarGz(t, files), runtime, "handler.handler")
	if status != http.StatusAccepted {
		t.Fatal("deploy native SQL function")
	}
	depID, _ := parseQueuedDeployment(t, body)
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, catalog, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatal("native SQL deployment did not become live")
	}
	snapshot := waitAfterRestoreSnapshot(t, store, depID, "")
	vmm, err := sched.DialVMMContext(ctx, h.VMMDSock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vmm.Close() }()
	scheduler, err := scheddgrpc.DialContext(ctx, h.ScheddSock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scheduler.Close() }()
	get := func(path string, want int, method string) nativeSQLReply {
		t.Helper()
		body, wake, status := nativeSQLHTTP(ctx, t, h.HTTPClient(), gatewayAppURL(h, slug)+path, slug+".apps.test.example")
		var reply nativeSQLReply
		if status != want || json.Unmarshal(body, &reply) != nil || reply.PID == 0 {
			t.Fatal("native SQL HTTP reply invalid")
		}
		if method != "" {
			if wake == "" {
				t.Fatal("native SQL wake omitted identity")
			}
			if _, err := e2etest.WaitForWakeMethod(ctx, t, catalog, wake, method, 10*time.Second); err != nil {
				t.Fatal(err)
			}
		}
		return reply
	}
	get("state", 200, "restore")
	instances, err := store.ListInstancesForApp(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	ins := pickState(instances, state.StateRunning)
	if ins.ID == "" {
		t.Fatal("native SQL function has no running VM")
	}
	lock := sql.Lock(ctx, t)
	pids := map[int]bool{}
	baseline := map[int]nativeSQLReply{}
	for _, id := range []string{"one", "two", "three"} {
		reply := get("tail/"+id, 202, "")
		pids[reply.PID], baseline[reply.PID] = true, reply
	}
	if len(pids) != 3 {
		t.Fatal("native SQL fixture did not occupy three interpreters")
	}
	backgroundNativeWait(t, ctx, func() bool { _, blocked := sql.Connections(ctx, t); return blocked == 3 })
	fourth := get("state", 200, "") // fourth interpreter, two additional pools
	baseline[fourth.PID] = fourth
	if _, err := vmm.FreezeInstance(ctx, ins.ID, false); err == nil {
		t.Fatal("froze active background SQL")
	}
	if err := scheduler.ParkInstance(ctx, ins.ID, "idle", ""); err == nil {
		t.Fatal("park/reuse interrupted active SQL")
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal("release native SQL lock")
	}
	backgroundNativeWait(t, ctx, func() bool { return len(sql.Rows(ctx, t)) == 3 && nativeSQLIdle(ctx, vmm, ins.ID) })
	backgroundNativeWait(t, ctx, func() bool { all, _ := sql.Connections(ctx, t); return all == 8 })
	if migrationScenario != "" {
		runNativeSQLMigration(t, ctx, store, vmm, sql, proxy, control, ins, baseline, get, migrationScenario)
		return
	}
	// Capture a warm artifact while retaining the original physical VM. The
	// independent peer holds both ends of the lifecycle; SQL sessions must
	// vanish before capture and return before the final recovery ACK.
	control.Arm()
	control.ArmRecovery()
	warmed := make(chan error, 1)
	go func() { warmed <- captureNativeSQLWarm(ctx, store, vmm, ins) }()
	backgroundNativeWait(t, ctx, func() bool {
		all, _ := sql.Connections(ctx, t)
		return control.Arrivals() == 4 && all == 0 && proxy.ConnectionCount() == 0
	})
	select {
	case err := <-warmed:
		t.Fatal("warm capture returned before drainage witness", err)
	default:
	}
	control.ReleaseCheckpoint()
	backgroundNativeWait(t, ctx, func() bool {
		all, _ := sql.Connections(ctx, t)
		return control.RecoveryArrivals() == 4 && all == 8
	})
	select {
	case err := <-warmed:
		t.Fatal("warm capture returned before recovery ACK", err)
	default:
	}
	control.Release()
	select {
	case err := <-warmed:
		if err != nil {
			t.Fatal("native SQL warm capture/recovery failed", err)
		}
	case <-ctx.Done():
		t.Fatal("native SQL warm capture did not finish")
	}
	continued := get("state", 200, "")
	requireNativeSQLGeneration(t, baseline[continued.PID], continued, 1)
	if len(sql.Rows(ctx, t)) != 3 {
		t.Fatal("continuing warm recovery lost or replayed SQL")
	}
	if err := store.MarkSnapshotStale(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	control.Arm()
	parked := make(chan error, 1)
	go func() { parked <- scheduler.ParkInstance(ctx, ins.ID, "idle", "") }()
	backgroundNativeWait(t, ctx, func() bool {
		all, _ := sql.Connections(ctx, t)
		return control.Arrivals() == 4 && all == 0 && proxy.ConnectionCount() == 0
	})
	select {
	case <-parked:
		t.Fatal("native capture completed before independent drainage witness")
	default:
	}
	control.Release()
	select {
	case err := <-parked:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("native SQL park did not finish")
	}
	_ = waitAfterRestoreSnapshot(t, store, depID, snapshot)
	restored := get("state", 200, "restore")
	requireNativeSQLGeneration(t, baseline[restored.PID], restored, 2)
	backgroundNativeWait(t, ctx, func() bool { all, _ := sql.Connections(ctx, t); return all == 8 })
	if len(sql.Rows(ctx, t)) != 3 {
		t.Fatal("restore lost or replayed SQL")
	}
	instances, err = store.ListInstancesForApp(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	ins = pickState(instances, state.StateRunning)
	backgroundNativeWait(t, ctx, func() bool { return nativeSQLIdle(ctx, vmm, ins.ID) })
	if _, err := vmm.FreezeInstance(ctx, ins.ID, false); err != nil {
		t.Fatal(err)
	}
	proxy.Disconnect()
	if _, err := vmm.ThawInstance(ctx, ins.ID, sched.ThawRequest{RunAfterRestore: true}); err != nil {
		t.Fatal(err)
	}
	thawed := get("state", 200, "")
	requireNativeSQLGeneration(t, baseline[thawed.PID], thawed, 3)
	backgroundNativeWait(t, ctx, func() bool { all, _ := sql.Connections(ctx, t); return all == 8 })
	// Both death paths run real SQL held behind an independent lock. A
	// successful replacement must never resubmit the interrupted mutation.
	for _, kind := range []string{"worker-crash", "runner-crash"} {
		faultLock := sql.Lock(ctx, t)
		dead := get(kind+"/"+kind, 202, "").PID
		backgroundNativeWait(t, ctx, func() bool {
			if kind == "worker-crash" {
				return nativeSQLIdle(ctx, vmm, ins.ID)
			}
			current, err := store.InstanceByID(ctx, ins.ID)
			return err == nil && !state.State(current.State).CountsForRAM()
		})
		if err := faultLock.Rollback(ctx); err != nil {
			t.Fatal("release native failure SQL lock")
		}
		method := ""
		if kind == "runner-crash" {
			method = "restore"
		}
		if reply := get("state", 200, method); kind == "worker-crash" && reply.PID == dead {
			t.Fatal("dead interpreter served again")
		}
		if rows := sql.Rows(ctx, t); len(rows) != 3 || rows[kind] != 0 {
			t.Fatal("native recovery replayed an interrupted SQL mutation")
		}
	}
}

// Use fresh owner-issued capture authority for measured runtimes. The legacy
// fixture path still lets the qualified host impose mandatory pool drainage.
func captureNativeSQLWarm(ctx context.Context, store *state.PgStore, vmm *sched.VMMClient, ins state.Instance) error {
	key := state.SnapshotCaptureMemKey(ins.DeploymentID, state.SnapshotTierWarm, uuid.NewString())
	stateKey := state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})
	admission, err := store.GetInstanceApplicationStandardAdmission(ctx, ins.ID)
	if err != nil {
		return err
	}
	if !admission.Managed {
		captured, err := vmm.WarmSnapshot(ctx, ins.ID, key, stateKey)
		return checkNativeSQLWarmCapture(captured, err)
	}
	snapshot, err := store.LatestSnapshot(ctx, ins.DeploymentID)
	if err != nil {
		return err
	}
	token, _ := state.SnapshotCaptureToken(key)
	grant, err := store.IssueApplicationStandardSnapshotCapture(ctx, string(state.StateRunning), state.ApplicationStandardSnapshotCaptureRequest{
		Token: token, InstanceID: ins.ID, MemoryKey: key, VMStateKey: stateKey,
		PrivateDriveKey: state.SnapshotDriveKey(state.Snapshot{StorageKey: key}), FCVersion: snapshot.FCVersion,
		Mode: "warm", BeforeCheckpoint: true, SourceStartedAtUnixNano: ins.StartedAt.UnixNano(),
	})
	if err != nil {
		return err
	}
	captured, _, err := vmm.CaptureAdmittedRuntime(ctx, grant)
	return checkNativeSQLWarmCapture(captured, err)
}

func checkNativeSQLWarmCapture(captured sched.SnapshotBytes, err error) error {
	if err != nil {
		return err
	}
	if captured.MemBytes <= 0 || captured.VMStateBytes <= 0 {
		return errors.New("native SQL warm capture omitted snapshot artifacts")
	}
	return nil
}

type nativeSQLReply struct {
	PID         int   `json:"pid"`
	Generations []int `json:"generations"`
}

func nativeSQLHTTP(ctx context.Context, t *testing.T, client *http.Client, url, host string) ([]byte, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal("create native SQL HTTP request")
	}
	req.Host = host
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("native SQL HTTP failed")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("read native SQL HTTP reply")
	}
	return body, response.Header.Get("x-faas-wake-id"), response.StatusCode
}

func requireNativeSQLGeneration(t *testing.T, before, after nativeSQLReply, delta int) {
	t.Helper()
	if before.PID == 0 || before.PID != after.PID || len(before.Generations) != 2 || len(after.Generations) != 2 {
		t.Fatal("native SQL recovery lost a loaded interpreter")
	}
	for i, generation := range before.Generations {
		if after.Generations[i] != generation+delta {
			t.Fatal("native SQL recovery did not rebuild both pool owners")
		}
	}
}

func nativeSQLIdle(ctx context.Context, vmm *sched.VMMClient, instance string) bool {
	stats, err := vmm.Stats(ctx)
	if err != nil {
		return false
	}
	for _, stat := range stats.Instances {
		if stat.InstanceID == instance {
			return stat.InflightRequests == 0
		}
	}
	return false
}
