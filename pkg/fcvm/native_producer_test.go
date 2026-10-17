//go:build linux

package fcvm

// adr: 2026101710
// Producer death and identity checks fence cleanup and recovery.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/onebox-faas/faas/pkg/datarepair"
)

func enrolledNativeProducer(t *testing.T) (*Manager, *JailerVMM, core.RehearsalFrame, Lease, nativeLaunchRecord, context.Context) {
	t.Helper()
	m, v, frame, _, _ := dataRepairNativeFixture(t)
	ctx := context.WithValue(t.Context(), dataRepairContextKey{}, frame)
	path, err := m.dataRepairPath(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeNativeJournalValue(path, dataRepairRecord{Version: 1, Frame: frame}); err != nil {
		t.Fatal(err)
	}
	lease := leaseForSlot(frame.TaskID, 0)
	lease.Plan, lease.Networkless = frame.Plan, true
	if _, err := m.prepareDataRepairNativeLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	record, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil || record.Version != 2 || record.Producer.PID != os.Getpid() {
		t.Fatal("producer was not enrolled before effects", err)
	}
	return m, v, frame, lease, record, ctx
}

func TestNativeProducerFramesAndCompatibility(t *testing.T) {
	_, v, _, lease, record, ctx := enrolledNativeProducer(t)
	j := v.nativeRecovery.journal
	if err := j.requireRepairProducerContext(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := v.nativeDriveStagingOwner(t.Context(), lease.Instance); err == nil {
		t.Fatal("compatibility staging borrowed repair ownership")
	}
	if _, err := j.beginPhysicalLaunch(t.Context(), lease); err == nil {
		t.Fatal("unscoped launch borrowed repair ownership")
	}
	b, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var copy nativeLaunchRecord
	if err := json.Unmarshal(b, &copy); err != nil || copy != record {
		t.Fatal("durable round trip changed value identity", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"missing", "null", "legacy", "protocol", "extra"} {
		t.Run(mutation, func(t *testing.T) {
			bad := make(map[string]json.RawMessage, len(fields))
			for k, v := range fields {
				bad[k] = v
			}
			switch mutation {
			case "missing":
				delete(bad, "producer")
			case "null":
				bad["producer"] = json.RawMessage(`null`)
			case "legacy":
				bad["version"] = json.RawMessage(`1`)
			case "protocol":
				bad["producer"] = json.RawMessage(`{"protocol":"unknown","pid":1,"uid":0,"start_time":1}`)
			case "extra":
				bad["producer"] = json.RawMessage(`{"protocol":"data-repair-coldboot-v1","pid":1,"uid":0,"start_time":1,"extra":true}`)
			}
			blob, _ := json.Marshal(bad)
			var decoded nativeLaunchRecord
			if err := json.Unmarshal(blob, &decoded); err == nil {
				t.Fatal("damaged producer frame accepted")
			}
		})
	}
	// No new fields are added to legacy frames, including nested helper owners.
	record.Version, record.Producer = 1, nativeProducerIdentity{}
	blob, _ := json.Marshal(record)
	if strings.Contains(string(blob), `"producer"`) || json.Unmarshal(blob, &copy) != nil {
		t.Fatal("legacy frame shape changed")
	}
}

func TestNativeProducerMissingTaskAndBindingCrashWindow(t *testing.T) {
	_, v, frame, lease, record, ctx := enrolledNativeProducer(t)
	j := v.nativeRecovery.journal
	path := filepath.Join(j.root, "data-repair", frame.TaskID+".json")
	parent, err := readDataRepairRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	// Crash after the physical record, before the outer binding is saved.
	parent.NativeGeneration, parent.NativeBootID, parent.NativeLease = "", "", Lease{}
	parent.Version, parent.NativeProducer = 1, nativeProducerIdentity{}
	if err := writeNativeJournalValue(path, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := j.records(t.Context()); err != nil {
		t.Fatal("pre-producer crash window cannot be inventoried", err)
	}
	if _, err := v.nativeDriveStagingOwner(ctx, lease.Instance); err == nil {
		t.Fatal("producer ran before original task binding")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := j.records(t.Context()); err == nil {
		t.Fatal("missing original task was ignored")
	}
	v.nativeRecovery.mu.Lock()
	delete(v.nativeRecovery.owned, lease.Instance)
	v.nativeRecovery.mu.Unlock()
	if err := v.requireNativeProducerRetirement(t.Context(), record); err == nil {
		t.Fatal("missing original ownership authorized cleanup")
	}
}

func TestNativeProducerHelperFailureRetainsResources(t *testing.T) {
	_, v, _, lease, owner, ctx := enrolledNativeProducer(t)
	groups := newNativeHelperGroupsFixture()
	v.nativeRecovery.helperGroups = groups
	j := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: groups}
	cmd, _ := nativeHelperFixtureCommand(t)
	if _, started, err := j.launch(ctx, owner, cmd); err != nil || !started {
		t.Fatal("helper launch", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	root := v.chrootRoot(lease.Instance)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.mu.Lock()
	delete(v.nativeRecovery.owned, lease.Instance)
	v.nativeRecovery.mu.Unlock()
	// Model the old daemon's absence independently of child retirement.
	v.nativeRecovery.producerExited = func(context.Context, nativeProducerIdentity) error { return nil }
	groups.retireErr = errors.New("kernel helper retirement unknown")
	if err := v.Kill(t.Context(), lease); err == nil {
		t.Fatal("uncertain helper cleanup acknowledged")
	}
	proof, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil || proof.ResourcesRemoved {
		t.Fatal("helper failure released resource ownership", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("helper failure removed the jail", err)
	}
	groups.retireErr = nil
	if err := v.Kill(t.Context(), lease); err != nil {
		t.Fatal("original helper cleanup did not retry", err)
	}
}

func TestNativeProducerChangedIncarnationCannotBecomeExitProof(t *testing.T) {
	_, v, _, lease, owner, _ := enrolledNativeProducer(t)
	v.nativeRecovery.mu.Lock()
	delete(v.nativeRecovery.owned, lease.Instance)
	v.nativeRecovery.mu.Unlock()
	// This value would appear to be a reused PID to the kernel probe. The
	// independent task binding must reject it before any resource is removed.
	owner.Producer.StartTime++
	if err := v.nativeRecovery.journal.write(owner); err != nil {
		t.Fatal(err)
	}
	if err := v.requireNativeProducerRetirement(t.Context(), owner); err == nil {
		t.Fatal("changed producer incarnation authorized cleanup")
	}
	if _, err := v.nativeRecovery.journal.records(t.Context()); err == nil {
		t.Fatal("recovery ignored the changed task/producer binding")
	}
}

func TestNativeProducerMissingLocalTokenDoesNotProveDaemonExit(t *testing.T) {
	_, v, _, lease, owner, _ := enrolledNativeProducer(t)
	v.nativeRecovery.mu.Lock()
	delete(v.nativeRecovery.owned, lease.Instance)
	v.nativeRecovery.mu.Unlock()
	if err := v.requireNativeProducerRetirement(t.Context(), owner); err == nil {
		t.Fatal("missing local token authorized cleanup while the original daemon lives")
	}
}

func TestNativeProducerPublicationFailureCannotStartGuest(t *testing.T) {
	m, v, frame, input, guest := dataRepairNativeFixture(t)
	v.nativeRecovery.journal.writeRecord = func(_ string, record nativeLaunchRecord) error {
		if record.Version != 2 || record.Producer.PID != os.Getpid() {
			t.Fatal("launch publication omitted original producer")
		}
		return errors.New("durable publication unavailable")
	}
	if evidence, err := m.RunDataRepairRehearsal(t.Context(), frame, input); err == nil || evidence.Passed {
		t.Fatal("unpublished ownership produced evidence")
	}
	if len(guest.coldBootSpecs) != 0 {
		t.Fatal("publication failure started a guest")
	}
}

type nativeProducerFixtureConfig struct {
	Frame      core.RehearsalFrame
	Root       string
	Ready      string
	WithHelper bool
}

func nativeProducerRealHelpers(t *testing.T, v *JailerVMM) {
	t.Helper()
	saved := cgroupRoot
	cgroupRoot = "/sys/fs/cgroup"
	t.Cleanup(func() { cgroupRoot = saved })
	v.nativeRecovery.helperGroups = newNativeHostHelperGroups()
}

// This subprocess is a real daemon lifetime and root lock, with a portable VM
// backend. Metal selects real cgroup containment for a surviving helper tree.
func TestNativeProducerOwnerProcess(t *testing.T) {
	path := os.Getenv("GREGALE_NATIVE_PRODUCER_CONFIG")
	if path == "" {
		t.Skip("subprocess fixture")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg nativeProducerFixtureConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	m, v, _ := nativeManagerFixture(t)
	v.chrootBase = cfg.Root
	v.nativeRecovery.journal.root = filepath.Join(cfg.Root, ".native-processes")
	if cfg.WithHelper {
		nativeProducerRealHelpers(t, v)
	}
	m.WithDataRepairRehearsals(cfg.Frame.NodeID, cfg.Frame.Runner)
	ctx := context.WithValue(t.Context(), dataRepairContextKey{}, cfg.Frame)
	ownedPath, err := m.dataRepairPath(ctx, cfg.Frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeNativeJournalValue(ownedPath, dataRepairRecord{Version: 1, Frame: cfg.Frame}); err != nil {
		t.Fatal(err)
	}
	lease := leaseForSlot(cfg.Frame.TaskID, 0)
	lease.Plan, lease.Networkless = cfg.Frame.Plan, true
	if _, err := m.prepareDataRepairNativeLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	root := v.chrootRoot(lease.Instance)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if cfg.WithHelper {
		owner, err := v.nativeRecovery.journal.read(lease.Instance)
		if err != nil {
			t.Fatal(err)
		}
		cmd, marker := nativeHelperFixtureCommand(t)
		t.Setenv("GREGALE_NATIVE_HOLD_HELPER_FIXTURE", "1")
		cmd.Env = os.Environ()
		j := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: v.nativeRecovery.helperGroups}
		if _, started, err := j.launch(ctx, owner, cmd); err != nil || !started {
			t.Fatal("start contained orphan fixture", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("contained helper did not cross its execution gate")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if err := os.WriteFile(cfg.Ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func runNativeProducerRecovery(t *testing.T, withHelper bool) {
	t.Helper()
	_, _, frame, _, _ := dataRepairNativeFixture(t)
	dir := t.TempDir()
	cfg := nativeProducerFixtureConfig{Frame: frame, Root: filepath.Join(dir, "jails"), Ready: filepath.Join(dir, "ready"), WithHelper: withHelper}
	blob, _ := json.Marshal(cfg)
	configPath := filepath.Join(dir, "producer.json")
	if err := os.WriteFile(configPath, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	m, v, _ := nativeManagerFixture(t)
	v.chrootBase = cfg.Root
	v.nativeRecovery.journal.root = filepath.Join(cfg.Root, ".native-processes")
	if withHelper {
		nativeProducerRealHelpers(t, v)
	}
	m.WithRuntimeAdmissionNodeID(frame.NodeID).WithDataRepairRehearsals(frame.NodeID, frame.Runner)
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativeProducerOwnerProcess$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "GREGALE_NATIVE_PRODUCER_CONFIG="+configPath, "GREGALE_NATIVE_LAUNCH_FIXTURE=0", "TMPDIR="+dir, "GORACE=atexit_sleep_ms=0")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output := &nativeHostHelperOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	t.Cleanup(func() {
		_ = input.Close()
		if !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := os.Stat(filepath.Join(v.nativeRecovery.journal.root, "data-repair", frame.TaskID+".json")); err == nil {
			if _, err := m.RetireDataRepairRehearsal(clean, frame); err != nil {
				t.Error("producer fixture cleanup unconfirmed:", err)
			}
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(cfg.Ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("producer never became ready: %s", output.Bytes())
		}
		time.Sleep(10 * time.Millisecond)
	}
	owner, err := v.nativeRecovery.journal.read(frame.TaskID)
	if err != nil || owner.Producer.PID != cmd.Process.Pid {
		t.Fatal("original producer identity missing", err)
	}
	if err := nativeProducerExited(t.Context(), owner.Producer); err == nil {
		t.Fatal("living original producer acknowledged")
	}
	reused := owner.Producer
	reused.StartTime++
	if err := nativeProducerExited(t.Context(), reused); err != nil {
		t.Fatal("PID reuse did not distinguish the original incarnation", err)
	}
	if err := nativeProducerExited(t.Context(), owner.Producer); err == nil {
		t.Fatal("reuse probe signalled the living process")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	joined = true
	if err := m.RecoverNativeProcesses(t.Context()); err != nil || m.alloc.InUse() != 1 {
		t.Fatal("restart failed to quarantine the physical lease", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.RetireDataRepairRehearsal(ctx, frame); err == nil || m.alloc.InUse() != 1 {
		t.Fatal("cancelled retirement released capacity", err)
	}
	if _, err := m.RetireDataRepairRehearsal(t.Context(), frame); err != nil {
		t.Fatal("restart did not retire the original attempt", err)
	}
	proof, err := v.nativeRecovery.journal.read(frame.TaskID)
	if err != nil || !proof.Revoked || !proof.ExitConfirmed || !proof.ResourcesRemoved || m.alloc.InUse() != 0 {
		t.Fatal("cleanup proof or capacity release missing", err)
	}
	if _, err := os.Stat(v.chrootRoot(frame.TaskID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original jail survived cleanup", err)
	}
	if withHelper {
		j := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: v.nativeRecovery.helperGroups}
		if err := j.requireRemoved(proof); err != nil {
			t.Fatal("original helper cgroup survived cleanup", err)
		}
	}
	if _, err := m.RetireDataRepairRehearsal(t.Context(), frame); err != nil {
		t.Fatal("retirement acknowledgement did not replay", err)
	}
}

func TestNativeProducerRealDaemonDeathRecovery(t *testing.T) {
	runNativeProducerRecovery(t, false)
}
