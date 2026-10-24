//go:build linux && metal

package fcvm

// adr: 20261017 vmmd restart adoption

// vmmd restart adoption ADR, validation experiments V-A1..V-A5 (slice 1).
//
// They answer how a vmmd restart really behaves before slice 2 depends on
// it. The "old vmmd" is this test binary in a transient systemd unit with
// faas-vmmd.service's sandbox (ProtectSystem=strict gives it a private mount
// namespace, as systemd gives vmmd). It boots a guest, records it and is
// then killed with SIGKILL, like a crash. A second unit, in a fresh mount
// namespace like a successor vmmd, probes the survivor. The results are
// facts, logged and written as JSON; only harness errors fail the test.
//
// Run on a dedicated acceptance host only:
//   FAAS_ADOPTION_EXPERIMENTS=1 go test -tags metal -run '^TestMetalAdoptionExperiments$' ./pkg/fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

const (
	adoptionExperimentRoleEnv = "FAAS_ADOPTION_EXPERIMENT_ROLE"
	adoptionExperimentDirEnv  = "FAAS_ADOPTION_EXPERIMENT_DIR"
	adoptionExperimentIDEnv   = "FAAS_ADOPTION_EXPERIMENT_INSTANCE"
)

// adoptionExperimentGuest is what the old vmmd records for its successor.
type adoptionExperimentGuest struct {
	Instance  string   `json:"instance"`
	PID       int      `json:"pid"`
	Netns     string   `json:"netns"`
	VethHost  string   `json:"veth_host"`
	JailRoot  string   `json:"jail_root"`
	Scope     string   `json:"scope"`
	OldMntNS  uint64   `json:"old_mnt_ns"`
	NetnsIno  uint64   `json:"netns_ino"`
	BindPaths []string `json:"bind_paths"`
	Slot      int      `json:"slot"`
}

func adoptionExperimentEnv(t *testing.T) []string {
	t.Helper()
	var env []string
	for _, key := range []string{"FAAS_TEST_KERNEL", "FAAS_TEST_BASE_ROOTFS", "FAAS_TEST_LAYER_ROOTFS", "FAAS_TEST_FC_VERSION", "FAAS_TEST_VMMD_BINARY", "FAAS_METAL_ACCEPTANCE_ROOT", "PATH"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, "--setenv="+key+"="+v)
		}
	}
	return env
}

// runAdoptionExperimentUnit runs this test binary in role inside a transient
// unit with vmmd's sandbox. wait=false starts it and returns its unit name.
func runAdoptionExperimentUnit(t *testing.T, dir, role, instance string, wait bool, extraEnv ...string) (string, string) {
	t.Helper()
	unit := "faas-adoption-exp-" + role + "-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--unit=" + unit, "--collect", "--quiet",
		"-p", "Slice=faas-cp.slice", "-p", "Delegate=yes", "-p", "NoNewPrivileges=yes",
		"-p", "ProtectSystem=strict", "-p", "ProtectHome=yes", "-p", "PrivateTmp=no",
		"-p", "ReadWritePaths=-/etc/faas/secrets /run/faas /run/netns /srv/fc /var/log/faas -/var/lib/faas/cache /sys/fs/cgroup/faas.slice " + dir,
		"--setenv=" + adoptionExperimentRoleEnv + "=" + role,
		"--setenv=" + adoptionExperimentDirEnv + "=" + dir,
		"--setenv=" + adoptionExperimentIDEnv + "=" + instance,
		"--setenv=TMPDIR=" + filepath.Join(dir, "tmp"),
	}
	args = append(args, adoptionExperimentEnv(t)...)
	for _, kv := range extraEnv {
		args = append(args, "--setenv="+kv)
	}
	if wait {
		args = append(args, "--wait", "--pipe")
	}
	args = append(args, self, "-test.run", "^TestMetalAdoptionExperimentRole$", "-test.v", "-test.timeout", "5m")
	out, err := exec.Command("systemd-run", args...).CombinedOutput()
	if wait && err != nil {
		t.Logf("%s output:\n%s", role, out)
		t.Fatalf("%s unit: %v", role, err)
	}
	if !wait && err != nil {
		t.Fatalf("start %s unit: %v: %s", role, err, out)
	}
	return unit, string(out)
}

func waitAdoptionExperimentFile(t *testing.T, path string, within time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s not written within %s", path, within)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestMetalAdoptionExperiments(t *testing.T) {
	if os.Getenv("FAAS_ADOPTION_EXPERIMENTS") != "1" {
		t.Skip("set FAAS_ADOPTION_EXPERIMENTS=1 on a dedicated acceptance host")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root and dedicated Linux KVM")
	}
	metalImages(t)
	dir, err := os.MkdirTemp("/var/tmp", "faas-adoption-exp-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	instance := uuid.NewString()
	t.Logf("experiment dir %s instance %s", dir, instance)
	version, _ := exec.Command("systemctl", "--version").Output()
	t.Logf("systemd: %s", strings.SplitN(string(version), "\n", 2)[0])

	// Old vmmd: boot, record, then crash.
	bootUnit, _ := runAdoptionExperimentUnit(t, dir, "boot", instance, false)
	var guest adoptionExperimentGuest
	if err := json.Unmarshal(waitAdoptionExperimentFile(t, filepath.Join(dir, "guest.json"), 2*time.Minute), &guest); err != nil {
		t.Fatal(err)
	}
	pidfd, err := unix.PidfdOpen(guest.PID, 0)
	if err != nil {
		t.Fatalf("pidfd for the guest before the crash: %v", err)
	}
	// Close inside the cleanup: a deferred close runs before t.Cleanup and
	// left the cleanup signalling a closed fd (the leaked guest of
	// 2026-10-17's run).
	t.Cleanup(func() {
		adoptionExperimentCleanup(t, guest, pidfd)
		_ = unix.Close(pidfd)
	})
	if out, err := exec.Command("systemctl", "kill", "--signal=SIGKILL", bootUnit).CombinedOutput(); err != nil {
		t.Fatalf("crash the old vmmd: %v: %s", err, out)
	}
	for i := 0; i < 100; i++ {
		if exec.Command("systemctl", "is-active", "--quiet", bootUnit).Run() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// V-A3 (second half): Firecracker keeps running with its stdout pipe's
	// reader gone. Give it time to write console output into the dead pipe.
	time.Sleep(3 * time.Second)
	survived := unix.PidfdSendSignal(pidfd, 0, nil, 0) == nil
	t.Logf("V-A3b firecracker survives the old vmmd's death (dead stdout pipe): %v", survived)

	// Successor: probe from a fresh mount namespace.
	_, out := runAdoptionExperimentUnit(t, dir, "probe", instance, true)
	t.Logf("probe output:\n%s", out)
	probe := waitAdoptionExperimentFile(t, filepath.Join(dir, "probe.json"), 10*time.Second)
	t.Logf("probe results: %s", probe)

	// V-A2: the systemd file descriptor store across a crash restart and an
	// explicit systemctl restart.
	adoptionExperimentFDStore(t, dir)
}

// TestMetalAdoptionRestartKeepsTheGuest is slice 2's real restart: the old
// vmmd boots a guest and is SIGKILLed; a successor in a fresh mount
// namespace adopts it, proves it owns it (same PID, API answers, guest
// channels re-bound), then destroys it through the ordinary teardown.
// Leakcheck must be clean afterwards with no manual cleanup.
func TestMetalAdoptionRestartKeepsTheGuest(t *testing.T) {
	if os.Getenv("FAAS_ADOPTION_EXPERIMENTS") != "1" {
		t.Skip("set FAAS_ADOPTION_EXPERIMENTS=1 on a dedicated acceptance host")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root and dedicated Linux KVM")
	}
	metalImages(t)
	dir, err := os.MkdirTemp("/var/tmp", "faas-adoption-restart-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	instance := uuid.NewString()
	bootUnit, _ := runAdoptionExperimentUnit(t, dir, "boot", instance, false)
	var guest adoptionExperimentGuest
	if err := json.Unmarshal(waitAdoptionExperimentFile(t, filepath.Join(dir, "guest.json"), 2*time.Minute), &guest); err != nil {
		t.Fatal(err)
	}
	pidfd, err := unix.PidfdOpen(guest.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	adopted := false
	t.Cleanup(func() {
		if !adopted || unix.PidfdSendSignal(pidfd, 0, nil, 0) == nil {
			adoptionExperimentCleanup(t, guest, pidfd)
		}
		_ = unix.Close(pidfd)
		leakcheck.AssertZero(t)
	})
	if out, err := exec.Command("systemctl", "kill", "--signal=SIGKILL", bootUnit).CombinedOutput(); err != nil {
		t.Fatalf("crash the old vmmd: %v: %s", err, out)
	}
	for i := 0; i < 100 && exec.Command("systemctl", "is-active", "--quiet", bootUnit).Run() == nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	_, out := runAdoptionExperimentUnit(t, dir, "adopt", instance, true)
	t.Logf("successor output:\n%s", out)
	var result map[string]any
	if err := json.Unmarshal(waitAdoptionExperimentFile(t, filepath.Join(dir, "adopt.json"), 10*time.Second), &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("successor result: %v", result)
	if result["adopted"] != true || result["same_pid"] != true || result["api_after_adoption"] != "Running" || result["destroyed"] != true {
		t.Fatalf("restart did not keep and then cleanly tear down the guest: %v", result)
	}
	adopted = true
	if unix.PidfdSendSignal(pidfd, 0, nil, 0) == nil {
		t.Fatal("guest still running after the successor's destroy")
	}
}

// adoptionExperimentAdoptRole is the successor vmmd: inventory, adoption,
// a check that it owns the guest, then an ordinary destroy.
func adoptionExperimentAdoptRole(t *testing.T, dir string) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	raw, err := os.ReadFile(filepath.Join(dir, "guest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g adoptionExperimentGuest
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	res := map[string]any{}
	defer func() {
		b, _ := json.Marshal(res)
		_ = os.WriteFile(filepath.Join(dir, "adopt.json"), b, 0o600)
	}()
	journal, err := OpenResourceJournal(filepath.Join(dir, "journal"))
	if err != nil {
		res["error"] = "journal: " + err.Error()
		return
	}
	m := NewAcceptanceManager(t)
	if err := m.WithResourceJournal(journal); err != nil {
		res["error"] = err.Error()
		return
	}
	vmm := m.vmm.(*JailerVMM).WithFileConsole()
	started := time.Now()
	if _, err := m.RecoverRestartQuarantine(ctx, vmm.JailRoot()); err != nil {
		res["error"] = "inventory: " + err.Error()
		return
	}
	rep, err := m.AdoptRestartSurvivors(ctx)
	res["adoption_ms"] = time.Since(started).Milliseconds()
	res["report"] = fmt.Sprintf("%+v", rep)
	if err != nil {
		res["error"] = "adopt: " + err.Error()
		return
	}
	res["adopted"] = rep.Adopted == 1 && m.HasInstanceOwnership(g.Instance)
	pid, ok := m.InstancePID(g.Instance)
	res["same_pid"] = ok && pid == g.PID
	if state, err := firecrackerAPIState(ctx, filepath.Join(g.JailRoot, APISockName)); err == nil {
		res["api_after_adoption"] = state.State
	} else {
		res["api_after_adoption"] = "error: " + err.Error()
	}
	if ns, err := resourceNetworkNamespaceAt(g.Netns); err == nil && ns != nil {
		res["netns_binding_after_adoption"] = ns.File.Inode == g.NetnsIno
	}
	if err := m.Destroy(ctx, g.Instance); err != nil {
		res["destroy_error"] = err.Error()
		return
	}
	res["destroyed"] = true
	if _, ok, err := journal.lookup(g.Instance); err == nil {
		res["record_retired"] = !ok
	}
}

func adoptionExperimentFDStore(t *testing.T, dir string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	unit := "faas-adoption-exp-fdstore-" + uuid.NewString()[:8]
	args := []string{"--unit=" + unit, "--quiet",
		"-p", "Type=notify", "-p", "NotifyAccess=main", "-p", "FileDescriptorStoreMax=8",
		"-p", "Restart=on-failure", "-p", "RestartSec=1",
		"--setenv=" + adoptionExperimentRoleEnv + "=fdstore",
		"--setenv=" + adoptionExperimentDirEnv + "=" + dir,
		self, "-test.run", "^TestMetalAdoptionExperimentRole$", "-test.v", "-test.timeout", "5m"}
	if out, err := exec.Command("systemd-run", args...).CombinedOutput(); err != nil {
		t.Fatalf("start fdstore unit: %v: %s", err, out)
	}
	defer func() {
		_ = exec.Command("systemctl", "stop", unit).Run()
		_ = exec.Command("systemctl", "reset-failed", unit).Run()
	}()
	crash := waitAdoptionExperimentFile(t, filepath.Join(dir, "fdstore-2.json"), 30*time.Second)
	t.Logf("V-A2 after a crash restart: %s", crash)
	if out, err := exec.Command("systemctl", "restart", unit).CombinedOutput(); err != nil {
		t.Logf("systemctl restart: %v: %s", err, out)
	}
	restart := waitAdoptionExperimentFile(t, filepath.Join(dir, "fdstore-3.json"), 30*time.Second)
	t.Logf("V-A2 after systemctl restart: %s", restart)
}

// adoptionExperimentCleanup tears the orphaned guest down by hand: nothing
// can adopt it yet. Leftovers are reported, then leakcheck runs.
func adoptionExperimentCleanup(t *testing.T, g adoptionExperimentGuest, pidfd int) {
	if err := unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		t.Errorf("kill the orphaned guest: %v", err)
	}
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, 10000); err != nil {
		t.Errorf("wait for guest exit: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	for _, p := range g.BindPaths {
		_ = unix.Unmount(p, unix.MNT_DETACH)
	}
	_ = unix.Unmount(filepath.Join("/run/netns", g.Netns), unix.MNT_DETACH)
	_ = os.Remove(filepath.Join("/run/netns", g.Netns))
	_ = os.RemoveAll(filepath.Dir(g.JailRoot))
	_ = os.Remove(g.Scope)
	_ = exec.Command("ip", "link", "delete", g.VethHost).Run()
	_ = os.Remove(filepath.Join("/var/log/faas", "vm-"+g.Instance+".console"))
	if out, err := exec.Command("bash", "-c", "ls /run/netns; ip -br link | grep -c '^vh' || true").CombinedOutput(); err == nil {
		t.Logf("after cleanup: %s", out)
	}
}

// TestMetalAdoptionExperimentRole is the body of each transient unit.
func TestMetalAdoptionExperimentRole(t *testing.T) {
	role := os.Getenv(adoptionExperimentRoleEnv)
	if role == "" {
		t.Skip("run only inside an adoption experiment unit")
	}
	dir := os.Getenv(adoptionExperimentDirEnv)
	switch role {
	case "boot":
		adoptionExperimentBoot(t, dir, os.Getenv(adoptionExperimentIDEnv))
	case "probe":
		adoptionExperimentProbe(t, dir)
	case "fdstore":
		adoptionExperimentFDStoreRole(t, dir)
	case "adopt":
		adoptionExperimentAdoptRole(t, dir)
	case "boot-kind":
		adoptionKindBootRole(t, dir, os.Getenv(adoptionExperimentIDEnv))
	case "adopt-kind":
		adoptionKindAdoptRole(t, dir)
	case "job-unit":
		adoptionJobUnitRole(t, dir, os.Getenv(adoptionExperimentIDEnv))
	default:
		t.Fatalf("unknown role %q", role)
	}
}

func adoptionExperimentBoot(t *testing.T, dir, instance string) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	journalDir := filepath.Join(dir, "journal")
	if err := os.Mkdir(journalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenResourceJournal(journalDir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewAcceptanceManager(t)
	if err := m.WithResourceJournal(journal); err != nil {
		t.Fatal(err)
	}
	if err := m.EnableAdoptionRecords(ctx); err != nil {
		t.Fatal(err)
	}
	// As vmmd does with adoption records on. FAAS_ADOPTION_PIPE_CONSOLE=1
	// keeps the pipe console, to reproduce V-A3.
	if os.Getenv("FAAS_ADOPTION_PIPE_CONSOLE") != "1" {
		m.vmm.(*JailerVMM).WithFileConsole()
	}
	req := WakeRequest{Instance: instance, BaseKey: os.Getenv("FAAS_TEST_BASE_ROOTFS"), LayerKey: os.Getenv("FAAS_TEST_LAYER_ROOTFS"), VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro}
	inst, err := m.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	recordAdoptionExperimentGuest(t, dir, m, journal, inst)
	// Hold everything, as a running vmmd would, until the driver kills this
	// unit. Cleanup must not run: the test is meant to die mid-flight.
	select {}
}

func adoptionExperimentProbe(t *testing.T, dir string) {
	raw, err := os.ReadFile(filepath.Join(dir, "guest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g adoptionExperimentGuest
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	res := map[string]any{}
	note := func(key string, v any) { res[key] = v; t.Logf("%s: %v", key, v) }
	if mnt, err := resourceMountNamespace(); err == nil {
		note("successor_mnt_ns_differs", mnt.Namespace != g.OldMntNS)
	}

	// V-A1: what the successor's mount namespace shows.
	ns, err := resourceNetworkNamespaceAt(g.Netns)
	switch {
	case err != nil:
		note("V-A1a netns binding", "error: "+err.Error())
	case ns == nil:
		note("V-A1a netns binding", "absent in successor namespace")
	default:
		note("V-A1a netns binding", fmt.Sprintf("visible, inode %d, matches process %v", ns.File.Inode, ns.File.Inode == g.NetnsIno))
	}
	visible := 0
	for _, p := range g.BindPaths {
		if m, err := resourceMountAt(p); err == nil && m != nil {
			visible++
		}
	}
	note("V-A1b image binds visible", fmt.Sprintf("%d of %d", visible, len(g.BindPaths)))
	var holders []string
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, e := range entries {
			if _, err := strconv.Atoi(e.Name()); err != nil {
				continue
			}
			if id, err := statIdentity(filepath.Join("/proc", e.Name(), "ns", "mnt")); err == nil && id.Inode == g.OldMntNS {
				comm, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
				holders = append(holders, e.Name()+":"+strings.TrimSpace(string(comm)))
			}
		}
	}
	note("V-A1c old vmmd mount namespace holders", holders)
	if id, err := statIdentity(filepath.Join("/proc", strconv.Itoa(g.PID), "ns", "mnt")); err == nil {
		note("V-A1c firecracker mount namespace is the old vmmd's", id.Inode == g.OldMntNS)
	}

	// V-A4: a non-parent pidfd and the executable identity.
	if fd, err := unix.PidfdOpen(g.PID, 0); err != nil {
		note("V-A4 non-parent pidfd_open", "error: "+err.Error())
	} else {
		note("V-A4 non-parent pidfd_open", "ok")
		note("V-A4 pidfd signal 0", fmt.Sprint(unix.PidfdSendSignal(fd, 0, nil, 0)))
		_ = unix.Close(fd)
	}
	if exe, err := statIdentity(filepath.Join("/proc", strconv.Itoa(g.PID), "exe")); err == nil {
		note("V-A4 exe inode", exe.Inode)
	}

	// V-A5: the Firecracker API from a new client, across pause and resume.
	socket := filepath.Join(g.JailRoot, APISockName)
	ctx := t.Context()
	state, err := firecrackerAPIState(ctx, socket)
	note("V-A5 GET / initial", fmt.Sprintf("%+v err=%v", state, err))
	for _, want := range []string{"Paused", "Resumed"} {
		if err := adoptionExperimentPatchVM(ctx, socket, want); err != nil {
			note("V-A5 PATCH /vm "+want, "error: "+err.Error())
			continue
		}
		state, err = firecrackerAPIState(ctx, socket)
		note("V-A5 GET / after "+want, fmt.Sprintf("%+v err=%v", state, err))
	}

	// The slice 1 dry run, from the successor's point of view.
	journal, err := OpenResourceJournal(filepath.Join(dir, "journal"))
	if err != nil {
		note("dry run", "journal: "+err.Error())
	} else {
		boot, _ := resourceKernelBootID("/proc")
		c := adoptionClassifier{procRoot: "/proc", cgroupRoot: "/sys/fs/cgroup", netRoot: "/sys/class/net", bootID: boot,
			api: firecrackerAPIState, pidfd: adoptionPidfdCheck, vethPeer: vethPeerInProcessNetns}
		rep, err := c.run(ctx, journal)
		note("dry run", fmt.Sprintf("adoptable=%d refused=%v err=%v verdicts=%+v", rep.Adoptable, rep.Refused, err, rep.Verdicts))
		_ = journal.Close()
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "probe.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func adoptionExperimentPatchVM(ctx context.Context, socket, state string) error {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, "http://localhost/vm", strings.NewReader(`{"state":"`+state+`"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// adoptionExperimentFDStoreRole: start 1 stores a listener in the fd store
// and crashes; start 2 records what came back and waits for a restart;
// start 3 records again and exits cleanly.
func adoptionExperimentFDStoreRole(t *testing.T, dir string) {
	countPath := filepath.Join(dir, "fdstore-count")
	n := 1
	if b, err := os.ReadFile(countPath); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		n++
	}
	_ = os.WriteFile(countPath, []byte(strconv.Itoa(n)), 0o600)
	got := map[string]string{"LISTEN_FDS": os.Getenv("LISTEN_FDS"), "LISTEN_FDNAMES": os.Getenv("LISTEN_FDNAMES"), "start": strconv.Itoa(n)}
	if n > 1 {
		b, _ := json.Marshal(got)
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("fdstore-%d.json", n)), b, 0o600)
	}
	if n == 1 {
		ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "fdstore.sock"), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		f, err := ln.File()
		if err != nil {
			t.Fatal(err)
		}
		if err := adoptionExperimentNotify("FDSTORE=1\nFDNAME=probe", int(f.Fd())); err != nil {
			t.Fatal(err)
		}
	}
	if err := adoptionExperimentNotify("READY=1", -1); err != nil {
		t.Fatal(err)
	}
	switch n {
	case 1:
		time.Sleep(time.Second)
		os.Exit(1) // a crash: Restart=on-failure restarts the unit
	case 2:
		select {} // wait for systemctl restart
	default:
		os.Exit(0)
	}
}

func adoptionExperimentNotify(state string, fd int) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return errors.New("NOTIFY_SOCKET unset")
	}
	if strings.HasPrefix(addr, "@") {
		addr = "\x00" + addr[1:]
	}
	sock, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(sock) }()
	var oob []byte
	if fd >= 0 {
		oob = unix.UnixRights(fd)
	}
	return unix.Sendmsg(sock, []byte(state), oob, &unix.SockaddrUnix{Name: addr}, 0)
}

// recordAdoptionExperimentGuest waits for the guest's adoption block and
// writes what the driver and the successor need to guest.json.
func recordAdoptionExperimentGuest(t *testing.T, dir string, m *Manager, journal *ResourceJournal, inst *Instance) {
	t.Helper()
	instance := inst.Lease.Instance
	pid, ok := m.InstancePID(instance)
	if !ok {
		t.Fatal("no guest pid")
	}
	var record resourceJournalRecord
	for deadline := time.Now().Add(10 * time.Second); ; {
		r, ok, err := journal.lookup(instance)
		if err != nil {
			t.Fatal(err)
		}
		if ok && r.Adoption != nil {
			record = r
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no adoption block")
		}
		time.Sleep(50 * time.Millisecond)
	}
	g := adoptionExperimentGuest{Instance: instance, PID: pid, Netns: inst.Lease.Netns, VethHost: inst.Lease.VethHost, Slot: inst.Lease.Slot,
		Scope: filepath.Join("/sys/fs/cgroup", vmCgroupParent(inst.Lease), PerInstanceScope(instance))}
	for _, a := range record.Assets {
		switch {
		case a.Kind == "jail" && filepath.Base(a.Path) == "root":
			g.JailRoot = a.Path
		case a.Kind == "bind":
			g.BindPaths = append(g.BindPaths, a.Path)
		}
	}
	if mnt, err := resourceMountNamespace(); err == nil {
		g.OldMntNS = mnt.Namespace
	}
	if id, err := statIdentity(filepath.Join("/proc", strconv.Itoa(pid), "ns", "net")); err == nil {
		g.NetnsIno = id.Inode
	}
	b, _ := json.Marshal(g)
	if err := os.WriteFile(filepath.Join(dir, "guest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("guest recorded: %s", b)
}
