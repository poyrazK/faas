//go:build metal && function_background_native

package e2e_test

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// This strict native gate exercises ordinary deployed functions, rather than
// private environment-qualification restores. Portable fault-injection and TLS
// SQL tests remain separate evidence; this test cannot qualify them by proxy.
func TestFunctionBackgroundMetal(t *testing.T) {
	requireFunctionBackgroundNative(t)
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Fatal("native function gate requires its disposable catalog")
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	h := e2etest.Start(t, pool, e2etest.All, append(secretSealingEnv(t), "FAAS_FUNCTION_RUNNER_NODE22="+os.Getenv("FAAS_E2E_FUNCTION_RUNNER_NODE22"))...)
	key := h.SeedAccount(t.Context(), api.PlanPro)
	slug := "background-" + randHexSuffix()
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "function", Runtime: "node22", RequireAuthn: &falsy, FunctionConcurrency: 1,
		BeforeCheckpoint: &api.BeforeCheckpointHook{Path: "/checkpoint", TimeoutMS: 2000},
		AfterRestore:     &api.AfterRestoreHook{Path: "/restore", TimeoutMS: 2000},
	}); got != http.StatusCreated {
		t.Fatalf("create function: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
		if _, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/"+slug, nil); status >= 300 {
			t.Errorf("retire native function: status=%d", status)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		backgroundNativeWait(t, ctx, func() bool {
			instances, err := state.NewPgStore(pool).ListInstancesForApp(ctx, appID)
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
	body, status := postBinaryFunctionDeployment(t, h, key, slug, buildTarGz(t, map[string]string{"handler.js": nativeBackgroundHandler}), "node22", "handler.handler")
	if status != http.StatusAccepted {
		t.Fatalf("deploy native function: status=%d", status)
	}
	depID, _ := parseQueuedDeployment(t, body)
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	snapshot := waitAfterRestoreSnapshot(t, store, depID, "")
	get := func(path string, want int) nativeBackgroundWitness {
		t.Helper()
		body, status := doGetWithHost(t, h.HTTPClient(), gatewayAppURL(h, slug)+strings.TrimPrefix(path, "/"), slug+".apps.test.example", 10*time.Second)
		var reply nativeBackgroundWitness
		if status != want || json.Unmarshal(body, &reply) != nil {
			t.Fatalf("native function %s: status=%d", path, status)
		}
		return reply
	}
	get("/prepare", 200)
	instances, err := store.ListInstancesForApp(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	ins := pickState(instances, state.StateRunning)
	if ins.ID == "" {
		t.Fatal("no running VM after preparation")
	}
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
	pids := map[int]bool{}
	for _, id := range []string{"one", "two", "three"} {
		pids[get("/hold?id="+id, 202).PID] = true
	}
	if len(pids) != 3 {
		t.Fatal("fixture did not keep three distinct interpreters occupied")
	}
	stats, err := vmm.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var held bool
	for _, stat := range stats.Instances {
		held = held || (stat.InstanceID == ins.ID && stat.InflightRequests >= 3 && !stat.QuietPaused)
	}
	if !held {
		t.Fatal("HTTP completion lost VM execution occupancy")
	}
	_, err = vmm.FreezeInstance(ctx, ins.ID, false)
	requireNativeExecutionBusy(t, err)
	memKey := state.SnapshotCaptureMemKey(depID, state.SnapshotTierWarm, uuid.NewString())
	_, err = vmm.WarmSnapshot(ctx, ins.ID, memKey, state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey}))
	requireNativeExecutionBusy(t, err)
	if err := scheduler.ParkInstance(ctx, ins.ID, "idle", ""); err == nil {
		t.Fatal("park reused an old snapshot while background work was registered")
	}
	if fresh, err := store.InstanceByID(ctx, ins.ID); err != nil || fresh.State != string(state.StateRunning) {
		t.Fatal("busy scheduler park released the running instance")
	}
	// Keep one interpreter slot available to release the independent witness.
	get("/release", 200)
	backgroundNativeWait(t, ctx, func() bool { return len(get("/witness", 200).Rows) == 3 })
	backgroundNativeWait(t, ctx, func() bool {
		stats, err := vmm.Stats(ctx)
		if err != nil {
			return false
		}
		for _, stat := range stats.Instances {
			if stat.InstanceID == ins.ID {
				return stat.InflightRequests == 0
			}
		}
		return false
	})
	if _, err := vmm.FreezeInstance(ctx, ins.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := vmm.ThawInstance(ctx, ins.ID, sched.ThawRequest{RunAfterRestore: true}); err != nil {
		t.Fatal(err)
	}
	if got := get("/witness", 200); !nativeBackgroundRowsOnce(got.Rows) || nativeDistinctWorkers(got.Restored) != 4 {
		t.Fatal("thaw lost a mutation or missed an interpreter callback")
	}
	// Force a real terminal capture of all loaded interpreters, not reuse.
	if err := store.MarkSnapshotStale(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.ParkInstance(ctx, ins.ID, "idle", ""); err != nil {
		t.Fatalf("idle terminal capture: %v", err)
	}
	_ = waitAfterRestoreSnapshot(t, store, depID, snapshot)
	if got := get("/witness", 200); !nativeBackgroundRowsOnce(got.Rows) || len(got.Checkpointed) != 4 || nativeDistinctWorkers(got.Checkpointed) != 4 || len(got.Restored) != 8 || nativeDistinctWorkers(got.Restored) != 4 {
		t.Fatal("capture/restore lost work, replayed work or missed a loaded interpreter")
	}
}

func nativeBackgroundRowsOnce(rows []string) bool {
	rows = slices.Clone(rows)
	slices.Sort(rows)
	return slices.Equal(rows, []string{"one", "three", "two"})
}

func nativeDistinctWorkers(pids []string) int {
	seen := map[string]bool{}
	for _, pid := range pids {
		seen[pid] = true
	}
	return len(seen)
}

type nativeBackgroundWitness struct {
	PID          int      `json:"pid"`
	Rows         []string `json:"rows"`
	Checkpointed []string `json:"checkpointed"`
	Restored     []string `json:"restored"`
}

func requireNativeExecutionBusy(t *testing.T, err error) {
	t.Helper()
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeInstanceBusy {
		t.Fatalf("active function capture/freeze did not refuse admission: %v", err)
	}
}

func backgroundNativeWait(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for !ready() {
		select {
		case <-waitCtx.Done():
			t.Fatal("native function lifecycle witness timed out")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func requireFunctionBackgroundNative(t *testing.T) {
	t.Helper()
	if os.Getenv("FAAS_REQUIRE_FUNCTION_BACKGROUND_METAL") != "1" || !functionlifetime.NativeQualificationBuild {
		t.Fatal("use make test-function-background-native; skips cannot qualify this suite")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("function background qualification requires root on native x86_64 Linux")
	}
	for _, path := range []string{"/dev/kvm", "/etc/faas/builder-acceptance-host"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("function background qualification requires KVM on an isolated acceptance host")
		}
	}
	var exit *exec.ExitError
	if err := exec.Command("systemd-detect-virt", "--quiet").Run(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatal("virtualized or unclassified hosts cannot qualify function background execution")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" || os.Getenv("FAAS_E2E_BIN_DIR") != "" {
		t.Fatal("native gate requires disposable SQL and freshly built daemons")
	}
	for _, key := range []string{"FAAS_TEST_KERNEL", "FAAS_BUILDER_BASE_PATH", "FAAS_GUEST_INIT", "FAAS_E2E_FUNCTION_RUNNER_NODE22"} {
		if info, err := os.Stat(os.Getenv(key)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("missing staged native prerequisite %s", key)
		}
	}
	for _, key := range []string{"FAAS_GUEST_INIT", "FAAS_E2E_FUNCTION_RUNNER_NODE22"} {
		info, err := buildinfo.ReadFile(os.Getenv(key))
		if err != nil {
			t.Fatalf("cannot inspect native capability in %s", key)
		}
		qualified := false
		for _, setting := range info.Settings {
			if setting.Key == "-tags" {
				tags := strings.Split(setting.Value, ",")
				qualified = slices.Contains(tags, "metal") && slices.Contains(tags, "function_background_native")
			}
		}
		if !qualified {
			t.Fatalf("%s lacks the native function lifetime build tags", key)
		}
	}
}

const nativeBackgroundHandler = `const fs=require('fs');
const root='/tmp/function-background-native-';
const read=(name)=>{try{return fs.readFileSync(root+name,'utf8').trim().split('\n').filter(Boolean)}catch{return []}};
const put=(name,value)=>fs.appendFileSync(root+name,value+'\n');
const reply=(statusCode)=>({statusCode,body:JSON.stringify({pid:process.pid,rows:read('rows'),checkpointed:read('checkpointed'),restored:read('restored')})});
exports.handler=async(event,ctx)=>{
 const u=new URL(event.path+'?'+(event.query||''),'http://fixture');
 if(u.pathname==='/checkpoint') {if(ctx.lifecycle_hook!=='before_checkpoint')return {statusCode:403};put('checkpointed',process.pid);return {statusCode:204};}
 if(u.pathname==='/restore') {if(ctx.lifecycle_hook!=='after_restore')return {statusCode:403};put('restored',process.pid);return {statusCode:204};}
 if(u.pathname==='/prepare') {for(const name of ['release','rows','checkpointed','restored']){try{fs.unlinkSync(root+name)}catch{}}return reply(200);}
 if(u.pathname==='/release') {fs.writeFileSync(root+'release','1');return reply(200);}
 if(u.pathname==='/hold') {
   const id=u.searchParams.get('id');
   ctx.waitUntil(new Promise((resolve,reject)=>{
     const timer=setInterval(()=>{if(fs.existsSync(root+'release')){clearInterval(timer);put('rows',id);resolve()}},25);
     ctx.signal.addEventListener('abort',()=>{clearInterval(timer);reject(ctx.signal.reason)},{once:true});
   }));
   return reply(202);
 }
 return reply(200);
};
`
