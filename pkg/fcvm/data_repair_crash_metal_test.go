//go:build metal && linux

package fcvm

// adr: 20261017
// Physical producer retirement must fence repair cleanup after daemon crashes.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	repairguest "github.com/onebox-faas/faas/pkg/datarepair/guest"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

type dataRepairCrashConfig struct {
	Phase, Kernel, FramePath, Ready string
	Paths                           dataRepairMetalPaths
	Frame                           core.RehearsalFrame
}

type dataRepairCrashCheckpoint struct {
	Phase       string
	Frame       core.RehearsalFrame
	Leased      int
	Live        int
	GuestPassed bool
}

// Only this test wrapper pauses lifecycle boundaries. Boot, vsock, journals,
// pidfds, helper cgroups and filesystem retirement all use production backends.
// The subprocess hosts vmmd's Manager; it is not the serving vmmd/gRPC binary.
type dataRepairCrashVMM struct {
	*JailerVMM
	pause       func(string) error
	frame       core.RehearsalFrame
	guestPassed atomic.Bool
}

func (v *dataRepairCrashVMM) BootColdBoot(ctx context.Context, l Lease, spec ColdBootSpec) error {
	if err := v.JailerVMM.BootColdBoot(ctx, l, spec); err != nil {
		return err
	}
	return v.pause("boot")
}

func (v *dataRepairCrashVMM) DialDataRepairRehearsal(ctx context.Context, l Lease) (net.Conn, error) {
	c, err := v.JailerVMM.DialDataRepairRehearsal(ctx, l)
	if err != nil {
		return nil, err
	}
	return &dataRepairCrashConn{Conn: c, owner: v}, nil
}

type dataRepairCrashConn struct {
	net.Conn
	owner    *dataRepairCrashVMM
	received []byte
}

func (c *dataRepairCrashConn) Read(b []byte) (int, error) {
	// RunDataRepairRehearsal reads only after the complete bounded request has
	// been written. The real guest owns this connection and may be executing
	// SQL or holding its result; the host has not accepted that result yet.
	if err := c.owner.pause("rehearsal"); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(b)
	if len(c.received)+n <= api.DataRepairMaxRequestBytes {
		c.received = append(c.received, b[:n]...)
		var result repairguest.Result
		f := c.owner.frame
		want := core.Evidence{Passed: true, PlanDigest: f.PlanDigest, SnapshotSHA256: f.SnapshotSHA256, ChecksSHA256: f.ChecksSHA256, Runner: core.MicroVMRunner}
		d := json.NewDecoder(bytes.NewReader(c.received))
		d.DisallowUnknownFields()
		if d.Decode(&result) == nil && d.Decode(new(any)) == io.EOF && result.FailureCode == "" && result.Evidence != nil && *result.Evidence == want {
			c.owner.guestPassed.Store(true)
		}
	}
	return n, err
}

func TestMetalDataRepairCrashRecovery(t *testing.T) {
	if os.Getenv("FAAS_DATA_REPAIR_NATIVE_TEST") != "1" {
		t.Skip("explicit native repair acceptance not selected")
	}
	if path := os.Getenv("GREGALE_REPAIR_CRASH_CONFIG"); path != "" {
		runDataRepairCrashOwner(t, path)
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	qualificationNativeHost(t, ctx)
	if os.Geteuid() != 0 {
		t.Fatal("repair crash acceptance requires root")
	}
	if os.Getenv("GREGALE_REPAIR_CRASH_ISOLATED") != "1" {
		// Owner and recovery processes share this mount namespace. The outer
		// host's mounts are never overmounted or adopted by the fixture.
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestMetalDataRepairCrashRecovery$", "-test.timeout=8m", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_REPAIR_CRASH_ISOLATED=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated repair crash acceptance: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return
	}
	for _, phase := range []string{"boot", "rehearsal", "cleanup", "cleanup_ack"} {
		t.Run(phase, func(t *testing.T) { dataRepairMetalCrashCase(t, phase) })
	}
}

func runDataRepairCrashOwner(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg dataRepairCrashConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || !slices.Contains([]string{"boot", "rehearsal", "cleanup", "cleanup_ack"}, cfg.Phase) {
		t.Fatal("invalid native crash configuration", err)
	}
	input, err := core.ReadRehearsalInput(os.Stdin)
	if err != nil || !cfg.Frame.Matches(input.Plan) || cfg.Frame.SnapshotSHA256 != input.SnapshotSHA256 {
		t.Fatal("invalid native crash input", err)
	}
	fixture := dataRepairMetalFixture{Kernel: cfg.Kernel, Runner: cfg.Frame.Runner, Input: input}
	m, v := newDataRepairMetalManager(t, fixture, &cfg.Paths, cfg.Frame.NodeID)
	_, incarnation, _, err := m.DataRepairRehearsalIdentity()
	if err != nil {
		t.Fatal(err)
	}
	f := cfg.Frame
	f.Incarnation = incarnation
	if err := writeNativeJournalValue(cfg.FramePath, f); err != nil {
		t.Fatal(err)
	}
	wrapped := &dataRepairCrashVMM{JailerVMM: v, frame: f}
	pause := func(phase string) error {
		if phase != cfg.Phase {
			return nil
		}
		checkpoint := dataRepairCrashCheckpoint{Phase: phase, Frame: f, Leased: m.LeasedCount(), Live: m.LiveCount(), GuestPassed: wrapped.guestPassed.Load()}
		if err := writeNativeJournalValue(cfg.Ready, checkpoint); err != nil {
			return err
		}
		<-t.Context().Done() // SIGKILL must bypass every Run/cleanup defer.
		return t.Context().Err()
	}
	v.nativeRecovery.journal.writeRecord = func(path string, record nativeLaunchRecord) error {
		if err := writeNativeLaunchRecord(path, record); err != nil {
			return err
		}
		if record.ResourcesRemoved {
			return pause("cleanup_ack")
		}
		if record.ExitConfirmed {
			return pause("cleanup")
		}
		return nil
	}
	wrapped.pause = pause
	m.vmm = wrapped
	if _, err := m.RunDataRepairRehearsal(t.Context(), f, input); err != nil {
		t.Fatal("native run ended before its crash checkpoint:", err)
	}
	t.Fatal("native run escaped its selected crash checkpoint")
}

type dataRepairCrashProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error // read only after done is closed
	output nativeHostHelperOutput
}

func startDataRepairCrashOwner(t *testing.T, path string, input core.RehearsalInput) *dataRepairCrashProcess {
	t.Helper()
	var body bytes.Buffer
	if err := core.WriteRehearsalInput(&body, input); err != nil {
		t.Fatal(err)
	}
	p := &dataRepairCrashProcess{cmd: exec.Command(os.Args[0], "-test.run=^TestMetalDataRepairCrashRecovery$", "-test.timeout=3m"), done: make(chan struct{})}
	p.cmd.Env = append(os.Environ(), "GREGALE_REPAIR_CRASH_CONFIG="+path, "GREGALE_NATIVE_LAUNCH_FIXTURE=0", "TMPDIR="+filepath.Dir(path), "GORACE=atexit_sleep_ms=0")
	p.cmd.Stdin = &body // Snapshot and SQL stay in a pipe, never in test journals.
	p.cmd.Stdout, p.cmd.Stderr = &p.output, &p.output
	p.cmd.WaitDelay = 2 * time.Second
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	return p
}

func (p *dataRepairCrashProcess) kill(ctx context.Context) error {
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-p.done:
		return p.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *dataRepairCrashProcess) checkpoint(t *testing.T, path string) dataRepairCrashCheckpoint {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-p.done:
			t.Fatalf("owner exited before crash checkpoint: %v\n%s", p.err, p.output.Bytes())
		case <-deadline.C:
			t.Fatalf("crash checkpoint timed out\n%s", p.output.Bytes())
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-tick.C:
			raw, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			var checkpoint dataRepairCrashCheckpoint
			if err != nil || json.Unmarshal(raw, &checkpoint) != nil || checkpoint.Frame.Validate() != nil {
				t.Fatal("invalid published crash checkpoint", err)
			}
			return checkpoint
		}
	}
}

func dataRepairMetalCrashCase(t *testing.T, phase string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	leakcheck.AssertZero(t)
	fixture := newDataRepairMetalFixture(t, ctx)
	p := newDataRepairMetalPaths(t, fixture.Runner.LayerKey)
	node := uuid.NewString()
	cfg := dataRepairCrashConfig{Phase: phase, Kernel: fixture.Kernel, Paths: *p, Frame: fixture.frame(node, uuid.NewString()), FramePath: filepath.Join(p.Disk, "frame.json"), Ready: filepath.Join(p.Disk, "ready.json")}
	path := filepath.Join(p.Disk, "owner.json")
	if err := writeNativeJournalValue(path, cfg); err != nil {
		t.Fatal(err)
	}
	before := dataRepairMetalSourceDigest(t, ctx, fixture)
	m, v := newDataRepairMetalManager(t, fixture, p, node)
	owner := startDataRepairCrashOwner(t, path, fixture.Input)
	p.keep = true
	t.Cleanup(func() {
		clean, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		_ = owner.kill(clean)
		select {
		case <-owner.done:
		default:
			t.Error("native crash owner was not joined")
			return
		}
		raw, err := os.ReadFile(cfg.FramePath)
		if err != nil {
			t.Error("original crash frame unavailable:", err)
			return
		}
		f, err := core.DecodeRehearsalFrame(raw)
		if err != nil {
			t.Error("original crash frame damaged:", err)
			return
		}
		// A task exists before any native effect. An earlier child failure may
		// leave only its handshake; in that case inventory must prove emptiness.
		if err := m.RecoverNativeProcesses(clean); err != nil {
			t.Error("failed crash fixture inventory:", err)
			return
		}
		if _, err := os.Stat(filepath.Join(v.nativeRecovery.journal.root, "data-repair", f.TaskID+".json")); err == nil {
			if _, err := m.RetireDataRepairRehearsal(clean, f); err != nil {
				t.Error("failed crash fixture retirement:", err)
				return
			}
		} else if !errors.Is(err, os.ErrNotExist) || m.LeasedCount() != 0 {
			t.Error("failed crash fixture has unresolved ownership:", err)
			return
		}
		p.keep = false
		leakcheck.AssertZero(t)
	})
	checkpoint := owner.checkpoint(t, cfg.Ready)
	f := checkpoint.Frame
	if checkpoint.Phase != phase || checkpoint.Leased != 1 || !f.Matches(fixture.Input.Plan) || f.NodeID != node || phase == "boot" && checkpoint.Live != 0 || phase == "rehearsal" && checkpoint.Live != 1 {
		t.Fatal("crash checkpoint did not retain the original physical lease")
	}
	// The checkpoint freezes the writer after atomic publication. Do not take
	// its physical lock: cleanup deliberately dies while holding that lock.
	physical, err := v.nativeRecovery.journal.read(f.TaskID)
	cleanup := phase == "cleanup" || phase == "cleanup_ack"
	if cleanup && !checkpoint.GuestPassed {
		t.Fatal("cleanup checkpoint followed a failed or incomplete guest rehearsal")
	}
	if err != nil || physical.Version != 2 || !physical.Authorized || physical.Producer.PID != owner.cmd.Process.Pid || physical.Revoked != cleanup || physical.ExitConfirmed != cleanup || physical.ResourcesRemoved != (phase == "cleanup_ack") {
		t.Fatal("selected physical crash boundary was not reached", err)
	}
	if err := nativeProducerExited(ctx, physical.Producer); err == nil {
		t.Fatal("live crash owner was treated as retired")
	}
	liveCtx, stopLive := context.WithTimeout(ctx, 25*time.Millisecond)
	err = m.RecoverNativeProcesses(liveCtx)
	stopLive()
	if err == nil {
		t.Fatal("replacement acquired the living owner's allocator root")
	}
	var vm nativeProcessHandle
	if !cleanup {
		identity, found, err := v.nativeRecovery.retirer.probe.process(physical.PID)
		if err != nil || !found || identity.Jailer || identity.Instance != f.TaskID || identity.StartTime != physical.StartTime || identity.UID != physical.Lease.UID {
			t.Fatal("checkpoint did not contain the original real Firecracker", err)
		}
		vm, err = openNativeProcess(physical.PID)
		if err != nil {
			t.Fatal(err)
		}
		defer vm.Close()
		requireDataRepairVMAlive(t, ctx, vm)
	}
	var killed *exec.ExitError
	err = owner.kill(ctx)
	if !errors.As(err, &killed) || killed.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("owner did not die by SIGKILL:", err)
	}
	if phase == "boot" {
		requireDataRepairVMAlive(t, ctx, vm)
	}
	verifyDataRepairCrashRecovery(t, ctx, m, v, f, physical, fixture.Input)
	if after := dataRepairMetalSourceDigest(t, ctx, fixture); before != after {
		t.Fatal("crash recovery changed source rows, versions or human edits")
	}
	saved, err := fixture.Engine.Get(ctx, f.Scope, f.PlanID)
	if err != nil || saved.RehearsedAt != nil || saved.ApprovedAt != nil || saved.AppliedAt != nil {
		t.Fatal("interrupted rehearsal published live repair authority", err)
	}
	qualificationAssets(t, map[string]string{fixture.Kernel: fixture.Runner.KernelSHA256, fixture.Runner.BaseKey: fixture.Runner.BaseSHA256, fixture.Runner.LayerKey: fixture.Runner.LayerSHA256})
}

func requireDataRepairVMAlive(t *testing.T, ctx context.Context, vm nativeProcessHandle) {
	t.Helper()
	probe, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	if err := vm.Wait(probe); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("original Firecracker was not alive at the crash boundary", err)
	}
}

func dataRepairMetalSourceDigest(t *testing.T, ctx context.Context, f dataRepairMetalFixture) [32]byte {
	t.Helper()
	var rows string
	if err := f.Engine.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(p) ORDER BY id)::text FROM products p`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256([]byte(rows))
}

func verifyDataRepairCrashRecovery(t *testing.T, ctx context.Context, m *Manager, v *JailerVMM, f core.RehearsalFrame, physical nativeLaunchRecord, input core.RehearsalInput) {
	t.Helper()
	wantHeld := 1
	if physical.ResourcesRemoved {
		wantHeld = 0 // A durable, independently checked receipt already exists.
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil || m.LeasedCount() != wantHeld || m.LiveCount() != 0 || len(m.cidToID) != 0 {
		t.Fatal("restart did not quarantine the original attempt without readiness", err)
	}
	_, incarnation, _, err := m.DataRepairRehearsalIdentity()
	if err != nil || incarnation == f.Incarnation {
		t.Fatal("restart reused the old daemon incarnation", err)
	}
	current := f
	current.Incarnation = incarnation
	current.Deadline = time.Now().UTC().Add(time.Duration(api.DataRepairTimeoutSeconds) * time.Second)
	for _, frame := range []core.RehearsalFrame{f, current} {
		if evidence, err := m.RunDataRepairRehearsal(ctx, frame, input); err == nil || evidence.Passed {
			t.Fatal("restart replayed the interrupted physical task")
		}
	}
	unchanged, err := v.nativeRecovery.journal.read(f.TaskID)
	if err != nil || unchanged != physical {
		t.Fatal("refused replay changed the original native generation", err)
	}
	if !physical.ResourcesRemoved {
		probeID := uuid.NewString()
		lease, err := m.alloc.Acquire(probeID)
		if err != nil || lease.Slot == physical.Lease.Slot {
			t.Fatal("unfinished repair slot was reused", err)
		}
		if err := m.alloc.Release(probeID); err != nil {
			t.Fatal(err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := m.RetireDataRepairRehearsal(cancelled, f); err == nil || m.LeasedCount() != 1 {
			t.Fatal("cancelled retirement released the original slot")
		}
	}
	at, err := m.RetireDataRepairRehearsal(ctx, f)
	if err != nil || at.IsZero() || m.LeasedCount() != 0 {
		t.Fatal("original crash attempt could not be retired", err)
	}
	proof, err := v.nativeRecovery.journal.read(f.TaskID)
	if err != nil || proof.Generation != physical.Generation || proof.PID != physical.PID || proof.StartTime != physical.StartTime || proof.Producer != physical.Producer || !proof.Revoked || !proof.ExitConfirmed || !proof.ResourcesRemoved {
		t.Fatal("retirement acknowledged another generation or incomplete cleanup", err)
	}
	//nolint:contextcheck // This existing resource-proof API uses vmmd's bounded private staging context.
	if err := v.nativeResourcesRemoved(proof.Lease, nativeLeaseNetwork(proof.Lease)); err != nil {
		t.Fatal("kernel resource absence was not established", err)
	}
	replayed, err := m.RetireDataRepairRehearsal(ctx, f)
	if err != nil || !replayed.Equal(at) {
		t.Fatal("lost retirement response did not replay its original timestamp", err)
	}
	m.alloc.mu.Lock()
	free := slices.Contains(m.alloc.free, physical.Lease.Slot)
	m.alloc.mu.Unlock()
	if !free {
		t.Fatal("confirmed cleanup did not return the original allocator slot")
	}
	if _, err := os.Stat(v.chrootRoot(f.TaskID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original crash jail remains", err)
	}
	t.Logf("%s: retired original generation %s after SIGKILL; source unchanged", f.TaskID, physical.Generation)
}

// This part of the native harness is executable without KVM: cleanup may be a
// success-path crash only after a complete, matching real guest response.
func TestDataRepairCrashResultWitness(t *testing.T) {
	_, _, f, _, _ := dataRepairNativeFixture(t)
	good := repairguest.Result{Evidence: &core.Evidence{Passed: true, PlanDigest: f.PlanDigest, SnapshotSHA256: f.SnapshotSHA256, ChecksSHA256: f.ChecksSHA256, Runner: core.MicroVMRunner}}
	encode := func(result repairguest.Result) []byte {
		b, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	wrong := good
	copyEvidence := *good.Evidence
	copyEvidence.PlanDigest = "different-plan"
	wrong.Evidence = &copyEvidence
	unknown := append([]byte(`{"unexpected":true,`), encode(good)[1:]...)
	for _, tc := range []struct {
		name string
		body []byte
		want bool
	}{
		{"matching_fragmented_result", encode(good), true},
		{"failed_guest", encode(repairguest.Result{FailureCode: "candidate_apply_and_checks"}), false},
		{"changed_plan", encode(wrong), false},
		{"unknown_fields", unknown, false},
		{"truncated_result", encode(good)[:10], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, guest := net.Pipe()
			defer host.Close()
			defer guest.Close()
			if err := host.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				defer guest.Close()
				for offset := 0; offset < len(tc.body); offset += 7 {
					if _, err := guest.Write(tc.body[offset:min(offset+7, len(tc.body))]); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			v := &dataRepairCrashVMM{frame: f, pause: func(string) error { return nil }}
			observed := &dataRepairCrashConn{Conn: host, owner: v}
			body, err := io.ReadAll(observed)
			if err != nil || !bytes.Equal(body, tc.body) {
				t.Fatal("crash observer changed guest transport", err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if v.guestPassed.Load() != tc.want {
				t.Fatal("invalid guest completion witness")
			}
		})
	}
}
