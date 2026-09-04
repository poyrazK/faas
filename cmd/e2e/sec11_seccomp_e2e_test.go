//go:build metal

// sec11_seccomp_e2e_test.go — M8 §11 cross-process seccomp gate.
//
// Spec §11: "Firecracker's default seccomp filter is in place." The
// pinned musl Firecracker release embeds its target-specific default
// filter and applies it in the Firecracker process; this test verifies
// the resulting kernel state rather than a custom filter-file path.
// Today NO test asserts that the filter survived a Firecracker binary
// or service-command regression — pkg/fcvm/manager_metal_test.go covers
// the in-process cgroup fence (PR A.1's neighbour), but the seccomp
// surface requires a real jailed Firecracker process.
//
// This test closes the gap by reading /proc/<pid>/status from the
// TEST process (cross-process, same as the memory.max fence test) for
// the jailer child of a live instance. The PID is resolved through
// the new SeccompStatus gRPC RPC (api/proto/onebox/faas/vmmd/v1,
// PR A.2) which itself reads the same /proc line server-side — but
// the e2e reads it again from the test process to make the contract
// explicit: the kernel state is the only thing that actually
// protects the box.
//
// Build tag: metal. Skips when /dev/kvm is absent, when
// FAAS_TEST_KERNEL is unset, or when the test binary is not running
// as uid 0 (jailer requires CAP_NET_ADMIN + CAP_MKNOD + seccomp).
//
// Failure modes caught:
//   - vmmd/service regression that disables Firecracker's default filter.
//   - vmmd regression that starts an unexpected Firecracker binary.
//   - Manager.InstancePID returning a stale PID (Kill ran but
//     destroy/cgroup cleanup raced).
//
// Spec anchor: §11 (jailer/VM), §14 M8 ("security checklist signed
// off item-by-item"), §4.4 (jailer as firecracker's only parent).

package e2e_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestSec11_SeccompFilterEnforced_CrossProcess is the M8 §11
// seccomp gate. Sequence:
//
//  1. Boot apid + schedd + imaged + vmmd + gatewayd as real
//     subprocesses via e2etest.Start(..., DeployWake). Same
//     harness A.1 uses; vmmd's BringUp spawns the jailer child
//     whose Firecracker child applies the embedded default filter.
//  2. Deploy a tiny app on Hobby plan; let the prime cycle
//     finish (cold-boot → snapshot → PARKED). The instance is
//     PARKED at the end of step 2 — no live jailer child to probe.
//  3. Wake the instance via the gatewayd hot path
//     (first-request-wakes-equivalent). The gateway has to make a
//     real HTTP request to wake the parked snapshot, which exercises
//     the schedd → vmmd → CreateFromSnapshot path that produces a
//     live jailer child. We WaitForInstanceState(running) to ensure
//     the child is alive before step 4.
//  4. Look up the instance ID from PG, then call the new
//     vmmd SeccompStatus gRPC RPC. The handler reads /proc/<pid>/status
//     server-side and returns the parsed mode + filter_len.
//  5. Independently read /proc/<pid>/status from the TEST process
//     (using the same PID the gRPC server returned). The two
//     paths MUST agree on Mode and FilterLen — if they don't, the
//     server-side read is buggy and the trust-on-first-use of the
//     wire is broken.
//  6. Assert Mode == "filter" and FilterLen > 0. FilterLen == 0
//     with Mode == "filter" means the kernel knows the seccomp
//     mode is "filter" but no BPF program is attached — exactly
//     the regression a disabled or unexpected Firecracker binary
//     would produce.
func TestSec11_SeccompFilterEnforced_CrossProcess(t *testing.T) {
	// Pre-flight: same conditions as PR A.1's memory.max test.
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal seccomp cross-process test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	vmmdSock := "/run/faas/vmmd.sock"
	if _, err := os.Stat(vmmdSock); err != nil {
		t.Skipf("vmmd socket not at %s: %v (harness must have started vmmd)", vmmdSock, err)
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Fake registry on loopback — same setup as deploy_wake_metal_test.
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	img, ref := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref = registry.AddImage("library/hello", img)

	// Issue #695 / ADR-080: post-flip Hobby defaults require_authn=true.
	// The doGetWithHost probes below hit the routed URL anonymously —
	// opt out at create-time.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "m8-seccomp", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "m8-seccomp")
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/m8-seccomp/deployments",
		api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var depResp api.DeploymentResponse
	if err := json.Unmarshal(raw, &depResp); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, raw)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer h.DumpLogs(t)
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depResp.ID, 60*time.Second); err != nil {
		if d, derr := state.NewPgStore(pool).DeploymentByID(ctx, depResp.ID); derr == nil {
			t.Logf("deployment state at failure: status=%s error=%q", d.Status, d.Error)
		}
		t.Fatalf("deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 60*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}

	// Wake the instance through the gatewayd hot path. The first
	// request after a PARKED state goes through schedd → vmmd's
	// CreateFromSnapshot, which spawns a fresh jailer child that
	// installs the seccomp filter before exec'ing firecracker.
	url := gatewayAppURL(h, "m8-seccomp")
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(context.Background(), t, client, url, 5*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	body, status := doGetWithHost(t, client, url, "m8-seccomp.apps.test.example", 30*time.Second)
	if status != http.StatusOK {
		t.Fatalf("wake: status=%d body=%s", status, body)
	}
	if got := strings.TrimSpace(string(body)); got != helloBody {
		t.Fatalf("wake body=%q want %q", got, helloBody)
	}

	// Instance is now RUNNING; the jailer child holds the seccomp
	// filter. Look up the instance ID from PG.
	store := state.NewPgStore(pool)
	inst, err := store.ListInstancesForApp(ctx, appID)
	if err != nil || len(inst) == 0 {
		t.Fatalf("no instances after wake: len=%d err=%v", len(inst), err)
	}
	var instanceID string
	for _, in := range inst {
		if in.State == string(state.StateRunning) {
			instanceID = in.ID
			break
		}
	}
	if instanceID == "" {
		t.Fatalf("no RUNNING instance found after wake; have %d instances", len(inst))
	}

	// Connect to vmmd over its unix socket and call SeccompStatus.
	// The harness exposes vmmd.sock under h.SockDir; the default
	// production path is /run/faas/vmmd.sock. Use the harness's
	// resolved path so a forked harness with a private SockDir
	// still works.
	sock := h.VMMDSock
	if sock == "" {
		sock = vmmdSock
	}
	conn, err := grpc.NewClient("unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return net.Dial("unix", sock)
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()
	clientV := vmmdpb.NewVmmdClient(conn)
	deadline, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dcancel()
	resp, err := clientV.SeccompStatus(deadline, &vmmdpb.SeccompStatusRequest{Instance: instanceID})
	if err != nil {
		t.Fatalf("SeccompStatus gRPC: %v", err)
	}
	if resp.GetError() != "" {
		t.Fatalf("SeccompStatus Error=%q (kernel read failed)", resp.GetError())
	}
	if resp.GetMode() != "filter" {
		t.Fatalf("SeccompStatus mode=%q want %q (Firecracker default seccomp filter missing)",
			resp.GetMode(), "filter")
	}
	if resp.GetFilterLen() <= 0 {
		t.Errorf("SeccompStatus filter_len=%d want >0 (kernel sees 'filter' mode but no BPF program attached)",
			resp.GetFilterLen())
	}

	// INDEPENDENT cross-process readback. Read /proc/<pid>/status
	// from the TEST process and verify the wire matches the
	// kernel state. This is the load-bearing assertion: it
	// catches a vmmd bug that returns mode="filter" without
	// actually reading /proc.
	procStatus := readProcStatus(t, int(resp.GetPid()))
	kernelMode, kernelFilterLen := parseSeccomp(procStatus)
	if kernelMode != resp.GetMode() {
		t.Errorf("wire mode=%q but kernel /proc says %q (SeccompStatus handler is reporting a phantom state)",
			resp.GetMode(), kernelMode)
	}
	if kernelFilterLen != resp.GetFilterLen() {
		t.Errorf("wire filter_len=%d but kernel /proc says %d",
			resp.GetFilterLen(), kernelFilterLen)
	}
	t.Logf("seccomp OK: instance=%s pid=%d mode=%s filter_len=%d (Firecracker default filter present)",
		instanceID, resp.GetPid(), resp.GetMode(), resp.GetFilterLen())
}

// readProcStatus returns the contents of /proc/<pid>/status. The
// status file is human-readable text the kernel writes for every
// process; this test treats it as the source of truth and cross-
// references the gRPC response against it.
func readProcStatus(t *testing.T, pid int) string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		t.Fatalf("read /proc/%d/status: %v", pid, err)
	}
	return string(b)
}

// parseSeccomp is the cross-process readback helper. It delegates to
// pkg/vmmdgrpc.ParseSeccompLines — the SAME parser the gRPC handler
// uses — so the test cannot drift from the production kernel-ABI
// reader. (An earlier revision inlined a duplicate parser here; the
// duplication was a tripwire: a kernel format change fixed in one
// place but not the other would silently disagree with vmmd's
// view, and the cross-process assertion would mask the regression.)
//
// The helper keeps the (mode, filterLen) two-return signature so
// the call site at line 237 (the cross-process check) stays a
// one-liner. Errors from the underlying parser are treated as
// ("unknown", 0) — the same behaviour the previous inline parser
// had for malformed input, so a missing Seccomp line in /proc
// surfaces as a kernel regression rather than a test panic.
func parseSeccomp(body string) (mode string, filterLen int32) {
	mode, filterLen, err := vmmdgrpc.ParseSeccompLines(strings.NewReader(body))
	if err != nil {
		return "unknown", 0
	}
	return mode, filterLen
}
