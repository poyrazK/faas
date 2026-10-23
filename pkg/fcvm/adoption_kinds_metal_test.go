//go:build linux && metal

package fcvm

// adr: 20261017 vmmd restart adoption

// Metal checks for the guest kinds adoption widened to (vmmd restart
// adoption ADR, slice 3b), on the transient-unit harness of
// adoption_experiments_metal_test.go: the old vmmd is SIGKILLed and a
// successor in a fresh mount namespace adopts.
//
//   - A warm-pool spare is adopted still paused and resumes on its claim.
//   - An app task is killed: process gone, record retired, slot free.
//   - A guest booted under an ADR-595 admission receipt, with
//     adopt_admitted_runtimes: a warm one is promoted under the successor
//     with a grant naming the successor; a measured (protocol 2) running
//     one is kept, but its capture is refused before any pause and the
//     guest torn down, because the predecessor's pinned drive handoff does
//     not survive the restart (docs/adr/20261018-runtime-admission-rebind.md,
//     open question 1).
//   - A job whose command exits while no vmmd runs: both vmmds are one
//     Type=notify unit with an fd store, restarted by Restart=on-failure, as
//     faas-vmmd.service is. The exit frame waits in the stored listener's
//     backlog and the successor returns it.
//
// Run on a dedicated acceptance host only:
//   FAAS_ADOPTION_EXPERIMENTS=1 go test -tags metal -run '^TestMetalAdoptionKinds$' ./pkg/fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/daemonunit"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const (
	adoptionKindEnv = "FAAS_ADOPTION_EXPERIMENT_KIND"
	// adoptionNodeEnv is the runtime-admission node identity both vmmds of
	// one scenario share, as a node keeps its identity across restarts.
	adoptionNodeEnv = "FAAS_ADOPTION_EXPERIMENT_NODE"
	// adoptionJobSleep is how long the job's command runs: past the old
	// vmmd's death and before the successor starts (adoptionJobRestartSec).
	adoptionJobSleep      = 6
	adoptionJobRestartSec = 20
	adoptionJobExitCode   = 3
	adoptionJobLeaseToken = "adoption-metal-lease"
)

func TestMetalAdoptionKinds(t *testing.T) {
	if os.Getenv("FAAS_ADOPTION_EXPERIMENTS") != "1" {
		t.Skip("set FAAS_ADOPTION_EXPERIMENTS=1 on a dedicated acceptance host")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root and dedicated Linux KVM")
	}
	metalImages(t)
	t.Run("warm spare stays paused", func(t *testing.T) {
		res := runAdoptionKind(t, "warm")
		if res["adopted"] != true || res["api_after_adoption"] != "Paused" || res["resumed"] != "Running" || res["destroyed"] != true {
			t.Fatalf("warm spare: %v", res)
		}
	})
	t.Run("app task is killed", func(t *testing.T) {
		res := runAdoptionKind(t, "apptask")
		if res["killed"] != true || res["live"] != float64(0) || res["record_retired"] != true || res["slot_free"] != true {
			t.Fatalf("app task: %v", res)
		}
	})
	t.Run("admitted warm spare promoted under the successor", func(t *testing.T) {
		res := runAdoptionKind(t, "admitted-warm")
		if res["adopted"] != true || res["held"] != true || res["api_after_adoption"] != "Paused" || res["promoted"] != "Running" ||
			res["receipt_names_successor"] != true || res["hold_ended"] != true || res["destroyed"] != true {
			t.Fatalf("admitted warm spare: %v", res)
		}
	})
	t.Run("measured admitted guest kept until its capture is refused", func(t *testing.T) {
		res := runAdoptionKind(t, "admitted-measured")
		if res["adopted"] != true || res["held"] != true || res["capture_refused"] != true || res["nothing_published"] != true || res["torn_down"] != true {
			t.Fatalf("measured admitted guest: %v", res)
		}
	})
	t.Run("job exit sent while no vmmd runs", func(t *testing.T) {
		res := runAdoptionJobUnit(t)
		if res["adopted"] != true || res["listener_stored"] != true || res["exit_code"] != float64(adoptionJobExitCode) ||
			res["lease_token"] != adoptionJobLeaseToken || res["destroyed"] != true {
			t.Fatalf("job: %v", res)
		}
	})
}

// runAdoptionKind boots one guest of kind in an old-vmmd unit, kills that
// unit and runs a successor that adopts and checks it.
func runAdoptionKind(t *testing.T, kind string) map[string]any {
	t.Helper()
	dir := adoptionKindDir(t)
	instance, node := uuid.NewString(), uuid.NewString()
	bootUnit, _ := runAdoptionExperimentUnit(t, dir, "boot-kind", instance, false, adoptionKindEnv+"="+kind, adoptionNodeEnv+"="+node)
	guest := waitAdoptionKindGuest(t, dir, bootUnit)
	pidfd := watchAdoptionKindGuest(t, guest)
	crashAdoptionUnit(t, bootUnit)
	_, out := runAdoptionExperimentUnit(t, dir, "adopt-kind", instance, true, adoptionKindEnv+"="+kind, adoptionNodeEnv+"="+node)
	t.Logf("successor output:\n%s", out)
	res := readAdoptionKindResult(t, dir)
	if unix.PidfdSendSignal(pidfd, 0, nil, 0) == nil {
		t.Errorf("guest still running after the successor finished with it: %v", res)
	}
	return res
}

func adoptionKindDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "faas-adoption-kind-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			_ = os.RemoveAll(dir)
		}
	})
	return dir
}

func waitAdoptionKindGuest(t *testing.T, dir, unit string) adoptionExperimentGuest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "guest.json")); err == nil && len(b) > 0 {
			var g adoptionExperimentGuest
			if err := json.Unmarshal(b, &g); err != nil {
				t.Fatal(err)
			}
			return g
		}
		if exec.Command("systemctl", "is-active", "--quiet", unit).Run() != nil && time.Now().After(deadline.Add(-170*time.Second)) {
			out, _ := exec.Command("journalctl", "-u", unit, "--no-pager", "-n", "80").CombinedOutput()
			t.Fatalf("old vmmd unit %s ended before recording its guest:\n%s", unit, out)
		}
		if time.Now().After(deadline) {
			t.Fatal("guest.json not written")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// watchAdoptionKindGuest holds a pidfd on the guest and, if the test fails
// with the guest still alive, tears it down by hand before leakcheck.
func watchAdoptionKindGuest(t *testing.T, g adoptionExperimentGuest) int {
	t.Helper()
	pidfd, err := unix.PidfdOpen(g.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if unix.PidfdSendSignal(pidfd, 0, nil, 0) == nil {
			adoptionExperimentCleanup(t, g, pidfd)
		}
		_ = unix.Close(pidfd)
		leakcheck.AssertZero(t)
	})
	return pidfd
}

func crashAdoptionUnit(t *testing.T, unit string) {
	t.Helper()
	if out, err := exec.Command("systemctl", "kill", "--signal=SIGKILL", unit).CombinedOutput(); err != nil {
		t.Fatalf("crash the old vmmd: %v: %s", err, out)
	}
	for i := 0; i < 100 && exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

func readAdoptionKindResult(t *testing.T, dir string) map[string]any {
	t.Helper()
	var res map[string]any
	if err := json.Unmarshal(waitAdoptionExperimentFile(t, filepath.Join(dir, "adopt.json"), 2*time.Minute), &res); err != nil {
		t.Fatal(err)
	}
	t.Logf("successor result: %v", res)
	return res
}

// adoptionKindManager is a vmmd with adoption records and the file console,
// over the journal in dir.
func adoptionKindManager(t *testing.T, ctx context.Context, dir string, fresh bool) (*Manager, *ResourceJournal) {
	t.Helper()
	journalDir := filepath.Join(dir, "journal")
	if fresh {
		if err := os.Mkdir(journalDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	journal, err := OpenResourceJournal(journalDir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewAcceptanceManager(t)
	if err := m.WithResourceJournal(journal); err != nil {
		t.Fatal(err)
	}
	m.vmm.(*JailerVMM).WithFileConsole().WithJobExitSink(m.RecordJobExit)
	if fresh {
		if err := m.EnableAdoptionRecords(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return m, journal
}

// adoptionKindBootRole is the old vmmd for one kind; it holds its guest
// until the driver kills it.
func adoptionKindBootRole(t *testing.T, dir, instance string) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	m, journal := adoptionKindManager(t, ctx, dir, true)
	m.WithRuntimeAdmissionNodeID(os.Getenv(adoptionNodeEnv))
	base, layer := os.Getenv("FAAS_TEST_BASE_ROOTFS"), os.Getenv("FAAS_TEST_LAYER_ROOTFS")
	var inst *Instance
	var err error
	switch kind := os.Getenv(adoptionKindEnv); kind {
	case "warm":
		inst, err = bootWarmSpare(ctx, m, dir, instance, base, layer)
	case "admitted-warm":
		inst, err = bootAdmittedWarm(t, ctx, m, dir, instance, base, layer)
	case "admitted-measured":
		inst, err = bootAdmittedMeasured(t, ctx, m, dir, instance, base, layer)
	case "apptask":
		inst, err = m.WakeAppTask(ctx, AppTaskWakeRequest{WakeRequest: WakeRequest{
			Instance: instance, AccountID: "acct-adopt", AppID: "app-adopt", DeploymentID: "dep-adopt",
			BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 256, CPUMillicores: 1000, Plan: api.PlanPro,
		}})
	default:
		t.Fatalf("unknown kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	recordAdoptionExperimentGuest(t, dir, m, journal, inst)
	select {}
}

// bootWarmSpare primes a snapshot from a cold boot, parks it and restores
// it paused, as the warm pool does.
func bootWarmSpare(ctx context.Context, m *Manager, dir, instance, base, layer string) (*Instance, error) {
	prime := uuid.NewString()
	if _, err := m.ColdBoot(ctx, ColdBootRequest{Instance: prime, Plan: api.PlanPro, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 256}); err != nil {
		return nil, fmt.Errorf("prime cold boot: %w", err)
	}
	key := "acceptance/adoption-warm-" + prime[:8]
	if err := os.MkdirAll(filepath.Join(adoptionAcceptanceRoot(), key), 0o2770); err != nil {
		return nil, err
	}
	vmstate := filepath.Join(dir, "vmstate")
	if _, err := m.Park(ctx, prime, SnapshotSpec{VMStatePath: vmstate, StorageKey: key + "/mem", VMStateStorageKey: key + "/vmstate"}); err != nil {
		return nil, fmt.Errorf("prime park: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot-key"), []byte(key), 0o600); err != nil {
		return nil, err
	}
	snap := &Snapshot{FCVersion: os.Getenv("FAAS_TEST_FC_VERSION"), StorageKey: key + "/mem", VMStateStorageKey: key + "/vmstate", VMStatePath: vmstate}
	return m.Wake(ctx, WakeRequest{Instance: instance, Plan: api.PlanPro, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 256, Snapshot: snap, KeepPaused: true})
}

// adoptionKindAdoptRole is the successor for one kind.
func adoptionKindAdoptRole(t *testing.T, dir string) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	res := map[string]any{}
	defer writeAdoptionKindResult(dir, res)
	g := readAdoptionKindGuest(t, dir)
	m, journal := adoptionKindManager(t, ctx, dir, false)
	m.WithRuntimeAdmissionNodeID(os.Getenv(adoptionNodeEnv)).WithAdoptAdmittedRuntimes(true)
	rep, ok := adoptSurvivors(ctx, m, res)
	if !ok {
		return
	}
	switch kind := os.Getenv(adoptionKindEnv); kind {
	case "admitted-warm", "admitted-measured":
		defer removeAdmittedArtifacts(dir)
		adoptAdmittedGuest(ctx, m, dir, g, rep, kind, res)
	case "warm":
		defer removeWarmSnapshot(dir)
		res["adopted"] = rep.Adopted == 1 && m.HasInstanceOwnership(g.Instance)
		res["api_after_adoption"] = apiState(ctx, g)
		if err := m.ResumeVM(ctx, g.Instance); err != nil {
			res["resume_error"] = err.Error()
		}
		res["resumed"] = apiState(ctx, g)
		if err := m.Destroy(ctx, g.Instance); err != nil {
			res["destroy_error"] = err.Error()
			return
		}
		res["destroyed"] = true
	case "apptask":
		res["killed"] = rep.Killed == 1 && rep.Adopted == 0
		res["live"] = m.LiveCount()
		_, ok, err := journal.lookup(g.Instance)
		res["record_retired"] = err == nil && !ok
		probe := uuid.NewString()
		if l, err := m.alloc.Acquire(probe); err == nil {
			res["slot_free"] = l.Slot == g.Slot
			_ = m.alloc.Release(probe)
		} else {
			res["slot_error"] = err.Error()
		}
	default:
		res["error"] = "unknown kind " + kind
	}
}

func adoptSurvivors(ctx context.Context, m *Manager, res map[string]any) (AdoptionDryRunReport, bool) {
	started := time.Now()
	if _, err := m.RecoverRestartQuarantine(ctx, m.vmm.(*JailerVMM).JailRoot()); err != nil {
		res["error"] = "inventory: " + err.Error()
		return AdoptionDryRunReport{}, false
	}
	rep, err := m.AdoptRestartSurvivors(ctx)
	res["adoption_ms"] = time.Since(started).Milliseconds()
	res["report"] = fmt.Sprintf("adopted=%d stopped=%d killed=%d refused=%v", rep.Adopted, rep.Stopped, rep.Killed, rep.Refused)
	for _, v := range rep.Verdicts {
		if v.Reason != "" {
			res["refusal"] = v.Reason + ": " + v.Detail
		}
	}
	if err != nil {
		res["error"] = "adopt: " + err.Error()
		return rep, false
	}
	return rep, true
}

func apiState(ctx context.Context, g adoptionExperimentGuest) string {
	state, err := firecrackerAPIState(ctx, filepath.Join(g.JailRoot, APISockName))
	if err != nil {
		return "error: " + err.Error()
	}
	return state.State
}

func readAdoptionKindGuest(t *testing.T, dir string) adoptionExperimentGuest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "guest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g adoptionExperimentGuest
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func writeAdoptionKindResult(dir string, res map[string]any) {
	b, _ := json.Marshal(res)
	_ = os.WriteFile(filepath.Join(dir, "adopt.json"), b, 0o600)
}

func removeWarmSnapshot(dir string) {
	key, err := os.ReadFile(filepath.Join(dir, "snapshot-key"))
	if err != nil || len(key) == 0 {
		return
	}
	_ = os.RemoveAll(filepath.Join(adoptionAcceptanceRoot(), string(key)))
}

// adoptionAcceptanceRoot is NewAcceptanceManager's storage root.
func adoptionAcceptanceRoot() string {
	if root := os.Getenv("FAAS_METAL_ACCEPTANCE_ROOT"); root != "" {
		return root
	}
	return "/srv/fc"
}

// runAdoptionJobUnit runs the job scenario as one restarting notify unit.
func runAdoptionJobUnit(t *testing.T) map[string]any {
	t.Helper()
	dir := adoptionKindDir(t)
	instance := uuid.NewString()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unit := "faas-adoption-exp-job-" + uuid.NewString()[:8]
	args := []string{"--unit=" + unit, "--quiet",
		"-p", "Type=notify", "-p", "NotifyAccess=main", "-p", "FileDescriptorStoreMax=8",
		"-p", "Restart=on-failure", "-p", "RestartSec=" + strconv.Itoa(adoptionJobRestartSec), "-p", "TimeoutStartSec=5min",
		"-p", "Slice=faas-cp.slice", "-p", "Delegate=yes", "-p", "NoNewPrivileges=yes",
		"-p", "ProtectSystem=strict", "-p", "ProtectHome=yes", "-p", "PrivateTmp=no",
		"-p", "ReadWritePaths=-/etc/faas/secrets /run/faas /run/netns /srv/fc /var/log/faas -/var/lib/faas/cache /sys/fs/cgroup/faas.slice " + dir,
		"--setenv=" + adoptionExperimentRoleEnv + "=job-unit",
		"--setenv=" + adoptionExperimentDirEnv + "=" + dir,
		"--setenv=" + adoptionExperimentIDEnv + "=" + instance,
		"--setenv=TMPDIR=" + filepath.Join(dir, "tmp"),
	}
	args = append(args, adoptionExperimentEnv(t)...)
	args = append(args, self, "-test.run", "^TestMetalAdoptionExperimentRole$", "-test.v", "-test.timeout", "8m")
	if out, err := exec.Command("systemd-run", args...).CombinedOutput(); err != nil {
		t.Fatalf("start job unit: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("journalctl", "-u", unit, "--no-pager", "-n", "120").CombinedOutput()
			t.Logf("job unit journal:\n%s", out)
		}
		_ = exec.Command("systemctl", "stop", unit).Run()
		_ = exec.Command("systemctl", "reset-failed", unit).Run()
	})
	guest := waitAdoptionKindGuest(t, dir, unit)
	pidfd := watchAdoptionKindGuest(t, guest)
	crashAdoptionUnit(t, unit)
	res := readAdoptionKindResult(t, dir)
	if unix.PidfdSendSignal(pidfd, 0, nil, 0) == nil {
		t.Errorf("job guest still running after the successor destroyed it: %v", res)
	}
	return res
}

// adoptionJobUnitRole is both vmmds of the job scenario: the first start
// boots the job and is killed; Restart=on-failure starts the successor.
func adoptionJobUnitRole(t *testing.T, dir, instance string) {
	countPath := filepath.Join(dir, "job-starts")
	n := 1
	if b, err := os.ReadFile(countPath); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		n++
	}
	_ = os.WriteFile(countPath, []byte(strconv.Itoa(n)), 0o600)
	names := os.Getenv("LISTEN_FDNAMES")
	store, err := daemonunit.OpenFDStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	if n == 1 {
		m, journal := adoptionKindManager(t, ctx, dir, true)
		m.vmm.(*JailerVMM).WithListenerStore(store)
		inst, err := m.BootJob(ctx, JobBootRequest{
			Instance: instance, AccountID: "acct-adopt", RunID: "run-adopt", Plan: api.PlanPro,
			KernelKey: os.Getenv("FAAS_TEST_KERNEL"), BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), ImageRef: os.Getenv("FAAS_TEST_LAYER_ROOTFS"),
			Command:   []string{"/bin/busybox", "sh", "-c", fmt.Sprintf("/bin/busybox sleep %d; exit %d", adoptionJobSleep, adoptionJobExitCode)},
			VcpuCount: 1, MemSizeMiB: 256, TaskTimeoutSec: 300, LeaseToken: adoptionJobLeaseToken,
		})
		if err != nil {
			t.Fatal(err)
		}
		recordAdoptionExperimentGuest(t, dir, m, journal, inst)
		if err := daemonunit.Notify("READY=1"); err != nil {
			t.Fatal(err)
		}
		select {}
	}
	if err := daemonunit.Notify("READY=1"); err != nil {
		t.Fatal(err)
	}
	res := map[string]any{"listen_fdnames": names}
	defer writeAdoptionKindResult(dir, res)
	g := readAdoptionKindGuest(t, dir)
	res["listener_stored"] = strings.Contains(names, jobExitStoreName(g.Instance))
	m, _ := adoptionKindManager(t, ctx, dir, false)
	m.vmm.(*JailerVMM).WithListenerStore(store)
	rep, ok := adoptSurvivors(ctx, m, res)
	if !ok {
		return
	}
	res["adopted"] = rep.Adopted == 1 && m.HasInstanceOwnership(g.Instance)
	res["untaken_listeners"] = len(store.Untaken())
	exit, err := m.WaitJobExit(ctx, g.Instance, time.Minute)
	if err != nil {
		res["wait_error"] = err.Error()
	} else {
		res["exit_code"], res["error_class"], res["lease_token"] = exit.ExitCode, exit.ErrorClass, exit.LeaseToken
	}
	// The retry the receiver acknowledged lets the guest power off now.
	pidfd, err := unix.PidfdOpen(g.PID, 0)
	if err == nil {
		fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
		started := time.Now()
		if n, _ := unix.Poll(fds, 30000); n == 1 {
			res["guest_powered_off_ms"] = time.Since(started).Milliseconds()
		}
		_ = unix.Close(pidfd)
	}
	if err := m.Destroy(ctx, g.Instance); err != nil {
		res["destroy_error"] = err.Error()
		return
	}
	res["destroyed"] = true
}

// admittedFacts is what the old vmmd hands the successor about its admitted
// guest: what schedd's grants would carry.
type admittedFacts struct {
	Parent         runtimeadmission.Receipt `json:"parent"`
	AppID          string                   `json:"app_id"`
	EgressRevision int64                    `json:"egress_revision"`
	Allowlist      []string                 `json:"allowlist"`
	Ports          []uint16                 `json:"ports"`
	Cleanup        []string                 `json:"cleanup"`
}

var admittedAllowlist = []string{"8.8.8.0/24"}

// admittedRequest is an admission grant for instance from this process, as
// schedd issues one, with the app's egress installed first.
func admittedRequest(ctx context.Context, m *Manager, req WakeRequest, protocol uint32) (AdmittedWakeRequest, error) {
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		return AdmittedWakeRequest{}, err
	}
	now := time.Now()
	b := runtimeadmission.Binding{ProtocolVersion: protocol, Token: uuid.NewString(), InstanceID: req.Instance, AppID: uuid.NewString(), DeploymentID: uuid.NewString(), AccountID: uuid.NewString(),
		NodeID: identity.NodeID, Incarnation: identity.Incarnation, DesiredRevision: 9, EffectiveHash: strings.Repeat("a", 64), CapturedInputHash: strings.Repeat("b", 64),
		PayloadHash: strings.Repeat("c", 64), EgressRevision: 7, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	if err := m.UpdateAppEgressPolicy(ctx, b.AppID, 7, []netip.Prefix{netip.MustParsePrefix(admittedAllowlist[0])}, []uint16{5432}); err != nil {
		return AdmittedWakeRequest{}, err
	}
	req.AppID, req.DeploymentID, req.AccountID, req.Plan = b.AppID, b.DeploymentID, b.AccountID, api.PlanPro
	req.EgressAllowlist, req.EgressPorts = slices.Clone(admittedAllowlist), []uint16{5432}
	if protocol == runtimeadmission.ArtifactProtocolVersion {
		if b.ArtifactSourcesHash, err = runtimeadmission.HashArtifactSources(req.ArtifactSources); err != nil {
			return AdmittedWakeRequest{}, err
		}
	}
	hash, err := NativeWakeInputHash(req)
	if err != nil {
		return AdmittedWakeRequest{}, err
	}
	return AdmittedWakeRequest{Request: req, Binding: b, NativeInputHash: hash}, nil
}

func recordAdmittedFacts(dir string, receipt runtimeadmission.Receipt, cleanup ...string) error {
	b, err := json.Marshal(admittedFacts{Parent: receipt, AppID: receipt.Binding.AppID, EgressRevision: receipt.Binding.EgressRevision,
		Allowlist: admittedAllowlist, Ports: []uint16{5432}, Cleanup: cleanup})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "admitted.json"), b, 0o600)
}

// bootAdmittedWarm restores an admitted warm spare paused from a primed
// snapshot (protocol 1), as the warm pool does for a managed app.
func bootAdmittedWarm(t *testing.T, ctx context.Context, m *Manager, dir, instance, base, layer string) (*Instance, error) {
	t.Helper()
	prime := uuid.NewString()
	if _, err := m.ColdBoot(ctx, ColdBootRequest{Instance: prime, Plan: api.PlanPro, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 256}); err != nil {
		return nil, fmt.Errorf("prime cold boot: %w", err)
	}
	key := "acceptance/adoption-admitted-" + prime[:8]
	if err := os.MkdirAll(filepath.Join(adoptionAcceptanceRoot(), key), 0o2770); err != nil {
		return nil, err
	}
	vmstate := filepath.Join(dir, "vmstate")
	if _, err := m.Park(ctx, prime, SnapshotSpec{VMStatePath: vmstate, StorageKey: key + "/mem", VMStateStorageKey: key + "/vmstate"}); err != nil {
		return nil, fmt.Errorf("prime park: %w", err)
	}
	snap := &Snapshot{FCVersion: os.Getenv("FAAS_TEST_FC_VERSION"), StorageKey: key + "/mem", VMStateStorageKey: key + "/vmstate", VMStatePath: vmstate}
	req, err := admittedRequest(ctx, m, WakeRequest{Instance: instance, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 256, Snapshot: snap, KeepPaused: true}, runtimeadmission.ProtocolVersion)
	if err != nil {
		return nil, err
	}
	inst, receipt, err := m.WakeAdmitted(ctx, req, nil)
	if err != nil {
		return nil, fmt.Errorf("admitted warm restore: %w", err)
	}
	return inst, recordAdmittedFacts(dir, receipt, filepath.Join(adoptionAcceptanceRoot(), key))
}

// bootAdmittedMeasured cold-boots an admitted guest on measured drives
// (protocol 2): the base and layer are published to storage first.
func bootAdmittedMeasured(t *testing.T, ctx context.Context, m *Manager, dir, instance, base, layer string) (*Instance, error) {
	t.Helper()
	backend, err := storage.NewLocalStorageBackend(adoptionAcceptanceRoot())
	if err != nil {
		return nil, err
	}
	prefix := "acceptance/adoption-measured-" + instance[:8]
	var sources []runtimeadmission.ArtifactSource
	for _, d := range []struct{ path, kind, key string }{{base, "base-image", prefix + "/base.ext4"}, {layer, "app-layer", prefix + "/layer.ext4"}} {
		src, err := putAdoptionDrive(ctx, backend, d.path, d.kind, d.key)
		if err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	req, err := admittedRequest(ctx, m, WakeRequest{Instance: instance, BaseKey: sources[0].StorageKey, LayerKey: sources[1].StorageKey,
		ArtifactSources: sources, VcpuCount: 1, MemSizeMiB: 256}, runtimeadmission.ArtifactProtocolVersion)
	if err != nil {
		return nil, err
	}
	inst, receipt, err := m.WakeAdmitted(ctx, req, nil)
	if err != nil {
		return nil, fmt.Errorf("measured admitted boot: %w", err)
	}
	return inst, recordAdmittedFacts(dir, receipt, filepath.Join(adoptionAcceptanceRoot(), prefix),
		filepath.Join(adoptionAcceptanceRoot(), "snap", receipt.Binding.DeploymentID))
}

// adoptAdmittedGuest checks the successor holds the admission and can serve
// it: it re-pushes the app's egress, as schedd's drift reconcile does, then
// promotes a warm spare or tries a capture of a measured guest.
func adoptAdmittedGuest(ctx context.Context, m *Manager, dir string, g adoptionExperimentGuest, rep AdoptionDryRunReport, kind string, res map[string]any) {
	raw, err := os.ReadFile(filepath.Join(dir, "admitted.json"))
	var facts admittedFacts
	if err == nil {
		err = json.Unmarshal(raw, &facts)
	}
	if err != nil {
		res["error"] = "admitted facts: " + fmt.Sprint(err)
		return
	}
	parent := facts.Parent
	res["adopted"] = rep.Adopted == 1 && m.HasInstanceOwnership(g.Instance)
	res["held"] = m.AdmissionHeldFrom(g.Instance, parent) == parent.Binding.Incarnation
	res["api_after_adoption"] = apiState(ctx, g)
	for _, v := range rep.Verdicts {
		res["report_admission"] = v.Admission != nil && v.Admission.Token == parent.Binding.Token
	}
	if err := m.UpdateAppEgressPolicy(ctx, facts.AppID, facts.EgressRevision, []netip.Prefix{netip.MustParsePrefix(facts.Allowlist[0])}, facts.Ports); err != nil {
		res["egress_error"] = err.Error()
	}
	successor, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		res["error"] = "identity: " + err.Error()
		return
	}
	now := time.Now()
	switch kind {
	case "admitted-warm":
		p := runtimeadmission.Promotion{Binding: parent.Binding, Parent: parent}
		p.Binding.Token, p.Binding.Incarnation = uuid.NewString(), successor.Incarnation
		p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = now.UnixNano(), now.Add(time.Minute).UnixNano()
		p.Binding.PayloadHash, _ = runtimeadmission.HashPromotionPayload(p.ToProto())
		_, r, err := m.PromoteAdmitted(ctx, p)
		if err != nil {
			res["promote_error"] = err.Error()
		}
		res["promoted"] = apiState(ctx, g)
		res["receipt_names_successor"] = err == nil && r.Binding.Incarnation == successor.Incarnation
		res["hold_ended"] = m.AdmissionHeldFrom(g.Instance, parent) == ""
	case "admitted-measured":
		// The predecessor's pinned drives did not survive the restart, so
		// the capture is refused before any pause; vmmd then tears the
		// guest down, as schedd's park sequence does on any capture error,
		// and the app's next wake cold-boots (ADR-005).
		mem := state.SnapshotCaptureMemKey(parent.Binding.DeploymentID, state.SnapshotTierWarm, uuid.NewString())
		token, _ := state.SnapshotCaptureToken(mem)
		grant := runtimeadmission.SnapshotGrant{Version: runtimeadmission.SnapshotGrantVersion, Token: token, Parent: parent,
			MemoryKey: mem, VMStateKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem}), PrivateDriveKey: state.SnapshotDriveKey(state.Snapshot{StorageKey: mem}),
			FCVersion: m.fcVersion, Mode: "warm", SourceStartedAtUnixNano: now.Add(-time.Minute).UnixNano(), IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
		_, _, err := m.CaptureAdmitted(ctx, grant)
		res["capture_error"] = fmt.Sprint(err)
		res["capture_refused"] = errors.Is(err, runtimeadmission.ErrUnavailable)
		published := false
		for _, key := range []string{grant.MemoryKey, grant.VMStateKey, grant.PrivateDriveKey} {
			if _, err := os.Stat(filepath.Join(adoptionAcceptanceRoot(), key)); !errors.Is(err, os.ErrNotExist) {
				published = true
			}
		}
		res["nothing_published"] = !published
		if _, live := m.LiveInstances()[g.Instance]; !live {
			res["torn_down"] = m.LeasedCount() == 0
			return
		}
	}
	if err := m.Destroy(ctx, g.Instance); err != nil {
		res["destroy_error"] = err.Error()
		return
	}
	res["destroyed"] = true
}

// putAdoptionDrive publishes a drive to storage with its measured identity.
func putAdoptionDrive(ctx context.Context, backend storage.StorageBackend, path, kind, key string) (runtimeadmission.ArtifactSource, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return runtimeadmission.ArtifactSource{}, err
	}
	defer file.Close()
	identity, err := rootfs.ReadArtifactIdentity(ctx, file)
	if err == nil {
		_, err = file.Seek(0, 0)
	}
	if err == nil {
		err = backend.Put(ctx, key, file)
	}
	if err != nil {
		return runtimeadmission.ArtifactSource{}, fmt.Errorf("publish %s: %w", kind, err)
	}
	return runtimeadmission.ArtifactSource{Kind: kind, StorageKey: key, Digest: identity.Digest, Bytes: identity.Bytes}, nil
}

func removeAdmittedArtifacts(dir string) {
	raw, err := os.ReadFile(filepath.Join(dir, "admitted.json"))
	var facts admittedFacts
	if err != nil || json.Unmarshal(raw, &facts) != nil {
		return
	}
	root := adoptionAcceptanceRoot() + "/"
	for _, p := range facts.Cleanup {
		if strings.HasPrefix(p, root) && len(p) > len(root) {
			_ = os.RemoveAll(p)
		}
	}
}
