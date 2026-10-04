//go:build linux || darwin

// adr: 532 — descriptor handoff and cleanup cannot borrow another VM incarnation.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

// File inheritance and launch gating are real. These ordinary files model
// kernel namespace/pidfd/device identities and provide no native acceptance.
type nativeJailBackendFixture struct {
	mu            sync.Mutex
	files         []*os.File
	prepares      int
	namespaceLive bool
	closeFailure  bool
	beforePrepare func()
}
type nativeJailInputsFixture struct {
	scope   jailsetup.DeviceSetupScope
	files   []*os.File
	backend *nativeJailBackendFixture
}

func (p *nativeJailInputsFixture) Scope() jailsetup.DeviceSetupScope { return p.scope }
func (p *nativeJailInputsFixture) Files() []*os.File                 { return p.files }
func (p *nativeJailInputsFixture) Close() error {
	var err error
	for _, file := range p.files {
		err = errors.Join(err, file.Close())
	}
	p.files = nil
	if p.backend.closeFailure {
		err = errors.Join(err, errors.New("injected descriptor close failure"))
	}
	return err
}
func (b *nativeJailBackendFixture) Prepare(_ context.Context, owner nativeLaunchRecord, root string, _ nativeTunSource) (nativeJailDeviceInputs, error) {
	if b.beforePrepare != nil {
		b.beforePrepare()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prepares++
	s := jailsetup.DeviceSetupScope{Generation: owner.Generation, BootID: owner.KernelBootID, PID: owner.PID, StartTime: owner.StartTime, UID: owner.Lease.UID, GID: owner.Lease.GID, ParentPID: os.Getpid(), ParentStartTime: 12345, RootMountID: 121, TunMountID: 122, TunMode: 0o666}
	identities := []*jailsetup.DeviceFDIdentity{&s.Root, &s.Namespace, &s.PIDHandle, &s.Tun}
	p := &nativeJailInputsFixture{backend: b}
	for i := range identities {
		file, err := os.CreateTemp(filepath.Dir(root), "device-input-")
		if err != nil {
			_ = p.Close()
			return nil, err
		}
		p.files = append(p.files, file)
		info, err := file.Stat()
		if err != nil {
			_ = p.Close()
			return nil, err
		}
		stat := info.Sys().(*syscall.Stat_t)
		*identities[i] = jailsetup.DeviceFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}
		s.ParentFDs[i] = int(file.Fd())
	}
	p.scope = s
	b.files = append(b.files, p.files...)
	b.namespaceLive = true
	return p, s.Validate()
}
func (b *nativeJailBackendFixture) InputsRemoved(_ context.Context, _ jailsetup.DeviceSetupScope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, file := range b.files {
		if _, err := file.Stat(); err == nil {
			return errors.New("fixture: original input remains open")
		}
	}
	return nil
}
func (b *nativeJailBackendFixture) NamespaceRemoved(_ context.Context, _ jailsetup.DeviceSetupScope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.namespaceLive {
		return errors.New("fixture: original namespace remains live")
	}
	return nil
}

func nativeJailDeviceFixture(t *testing.T) (*nativeHostHelperJournal, nativeLaunchRecord, *nativeJailBackendFixture, string) {
	t.Helper()
	tun, owner, _, root := nativeTunJournalFixture(t)
	if err := tun.stage(t.Context(), owner, root); err != nil {
		t.Fatal(err)
	}
	owner.Authorized, owner.PID, owner.StartTime = true, 999, 456
	if err := tun.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	backend := &nativeJailBackendFixture{}
	tun.owner.jailDevices = backend
	groups := newNativeHelperGroupsFixture()
	j := &nativeHostHelperJournal{owner: tun.owner, groups: groups, startTime: func(int) (uint64, error) { return 2345, nil }, purpose: nativeHostHelperJailDevices, deviceRoot: root}
	return j, owner, backend, root
}
func nativeJailFixtureCommand(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	_, marker := nativeHelperFixtureCommand(t)
	cmd, err := newNativeJailDeviceCommand(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return cmd, marker
}

func TestNativeJailDeviceHandoffClosesInputsBeforeAuthorizationAndRequiresReceipt(t *testing.T) {
	j, owner, backend, _ := nativeJailDeviceFixture(t)
	cmd, marker := nativeJailFixtureCommand(t)
	var output nativeHostHelperOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	j.writeValue = func(path string, record nativeHostHelperRecord) error {
		if record.Launch.Authorized {
			if !record.JailDevice.InputsClosed {
				t.Error("gate authorization preceded input closure")
			}
			if err := backend.InputsRemoved(t.Context(), record.JailDevice.Scope); err != nil {
				t.Error(err)
			}
		}
		return writeNativeJournalValue(path, record)
	}
	if err := j.run(t.Context(), owner, cmd, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("gated FD fixture did not execute:", err)
	}
	if err := j.requireDeviceReady(owner); err == nil {
		t.Fatal("successful helper exit substituted for receipt")
	}
	if err := j.confirmDeviceReceipt(t.Context(), owner, output.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := j.requireDeviceReady(owner); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err == nil {
		t.Fatal("live original namespace released VM ownership")
	}
	retry, _ := nativeJailFixtureCommand(t)
	if _, _, err := j.launch(t.Context(), owner, retry); err == nil {
		t.Fatal("completed setup authorized a second attachment")
	}
	if backend.prepares != 1 {
		t.Fatal("setup retried its kernel producer")
	}
}

func TestNativeJailDevicePinningHoldsOriginalProducerLock(t *testing.T) {
	j, owner, backend, _ := nativeJailDeviceFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	backend.beforePrepare = func() { close(entered); <-release }
	cmd, _ := nativeJailFixtureCommand(t)
	done := make(chan error, 1)
	go func() { done <- j.run(t.Context(), owner, cmd, time.Second) }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err := j.owner.revoke(ctx, owner.Lease.Instance)
	cancel()
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("revocation passed an active original FD producer: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := j.owner.revoke(t.Context(), owner.Lease.Instance); err != nil {
		t.Fatal(err)
	}
	late, _ := nativeJailFixtureCommand(t)
	if _, _, err := j.launch(t.Context(), owner, late); err == nil {
		t.Fatal("revoked original producer acquired a new handoff")
	}
	if backend.prepares != 1 {
		t.Fatal("late producer borrowed current pinning authority")
	}
}

func TestNativeJailDevicePublicationFailuresRetainRecoverableOriginalScope(t *testing.T) {
	for _, phase := range []string{"planned", "cgroup", "authorized", "closed"} {
		t.Run(phase, func(t *testing.T) {
			j, owner, backend, _ := nativeJailDeviceFixture(t)
			cmd, marker := nativeJailFixtureCommand(t)
			backend.closeFailure = phase == "closed"
			j.writeValue = func(path string, record nativeHostHelperRecord) error {
				if phase == "planned" && record.Group.Inode == 0 || phase == "cgroup" && record.Group.Inode != 0 && !record.Launch.Authorized || phase == "authorized" && record.Launch.Authorized {
					return errors.New("injected publication failure")
				}
				return writeNativeJournalValue(path, record)
			}
			if err := j.run(t.Context(), owner, cmd, time.Second); err == nil {
				t.Fatal("publication failure ignored")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unpublished handoff released child gate")
			}
			if err := backend.InputsRemoved(t.Context(), jailsetup.DeviceSetupScope{}); err != nil {
				t.Fatal(err)
			}
			restarted := &nativeHostHelperJournal{owner: j.owner, groups: j.groups}
			if phase != "planned" {
				if err := restarted.retireAll(t.Context(), owner); err != nil {
					t.Fatal(err)
				}
				if err := restarted.requireRemoved(owner); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNativeJailDeviceRecoveryCannotAcknowledgeOpenProducerInputs(t *testing.T) {
	j, owner, backend, root := nativeJailDeviceFixture(t)
	inputs, err := backend.Prepare(t.Context(), owner, root, nativeTunSource{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inputs.Close(); err != nil {
			t.Error(err)
		}
	})
	id := "77a0f451-7601-4a48-aabe-389ddf127e0f"
	group, err := j.groups.Plan(id)
	if err != nil {
		t.Fatal(err)
	}
	group, err = j.groups.Create(group)
	if err != nil {
		t.Fatal(err)
	}
	frame := nativeHostHelperRecord{OwnerGeneration: owner.Generation, Group: group, Purpose: nativeHostHelperJailDevices,
		Launch:     nativeLaunchRecord{Version: 1, Generation: id, KernelBootID: owner.KernelBootID, Lease: owner.Lease},
		JailDevice: &nativeJailDeviceFrame{Scope: inputs.Scope()}}
	if err := os.MkdirAll(j.root(owner.Generation), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := j.write(owner, frame); err != nil {
		t.Fatal(err)
	}
	restarted := &nativeHostHelperJournal{owner: j.owner, groups: j.groups}
	if err := restarted.retireAll(t.Context(), owner); err == nil || !strings.Contains(err.Error(), "input remains open") {
		t.Fatalf("producer input closure was assumed: %v", err)
	}
	if err := restarted.requireRemoved(owner); err == nil {
		t.Fatal("helper cgroup absence substituted for producer descriptor closure")
	}
	if err := inputs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.retireAll(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	frames, err := restarted.records(owner)
	if err != nil || len(frames) != 1 || !frames[0].JailDevice.InputsClosed || !frames[0].Launch.ResourcesRemoved || frames[0].JailDevice.Scope != inputs.Scope() {
		t.Fatalf("original descriptor retirement was not retained: %+v %v", frames, err)
	}
}

func TestNativeJailDeviceFrameRejectsAmbiguousOrExcessAuthority(t *testing.T) {
	j, owner, _, _ := nativeJailDeviceFixture(t)
	cmd, _ := nativeJailFixtureCommand(t)
	if err := j.run(t.Context(), owner, cmd, time.Second); err != nil {
		t.Fatal(err)
	}
	frames, err := j.records(owner)
	if err != nil || len(frames) != 1 {
		t.Fatalf("original helper frame: %+v %v", frames, err)
	}
	data, err := json.Marshal(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, damaged := range []string{
		strings.Replace(string(data), `"inputs_closed":true`, `"inputs_closed":null`, 1),
		strings.Replace(string(data), `,"inputs_closed":true`, "", 1),
		strings.Replace(string(data), `"inputs_closed":true`, `"inputs_closed":true,"inputs_closed":false`, 1),
		strings.Replace(string(data), `"inputs_closed":true`, `"inputs_closed":false`, 1),
		strings.Replace(string(data), `"inputs_closed":true`, `"inputs_closed":true,"receipt":null`, 1),
		strings.Replace(string(data), `"purpose":"jail_devices"`, `"purpose":"effect"`, 1),
		string(data) + `{}`,
	} {
		var frame nativeHostHelperRecord
		err := json.Unmarshal([]byte(damaged), &frame)
		if err == nil {
			err = frame.validate(owner)
		}
		if err == nil {
			t.Fatalf("ambiguous or excess device authority accepted: %s", damaged)
		}
	}
}

func TestNativeJailDeviceReceiptCannotBorrowAnotherScopeOrSurviveRevocation(t *testing.T) {
	for _, change := range []string{"namespace", "root", "pid", "generation", "revoked"} {
		t.Run(change, func(t *testing.T) {
			j, owner, _, _ := nativeJailDeviceFixture(t)
			cmd, _ := nativeJailFixtureCommand(t)
			var out nativeHostHelperOutput
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := j.run(t.Context(), owner, cmd, time.Second); err != nil {
				t.Fatal(err)
			}
			var receipt jailsetup.DeviceSetupReceipt
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "namespace":
				receipt.Scope.Namespace.Inode++
			case "root":
				receipt.Scope.Root.Inode++
			case "pid":
				receipt.Scope.StartTime++
			case "generation":
				receipt.Scope.Generation = "d3124d9a-657b-4f37-a9df-f5dfb2e50764"
			case "revoked":
				if _, err := j.owner.revoke(t.Context(), owner.Lease.Instance); err != nil {
					t.Fatal(err)
				}
			}
			data, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.confirmDeviceReceipt(t.Context(), owner, data); err == nil {
				t.Fatal("changed authority published readiness")
			}
		})
	}
}

func TestNativeJailDeviceNamespaceAndParentInputsHoldCleanup(t *testing.T) {
	j, owner, backend, _ := nativeJailDeviceFixture(t)
	cmd, _ := nativeJailFixtureCommand(t)
	if err := j.run(t.Context(), owner, cmd, time.Second); err != nil {
		t.Fatal(err)
	}
	owner.Revoked, owner.ExitConfirmed = true, true
	if err := j.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("live namespace bypassed acknowledgement: %v", err)
	}
	backend.namespaceLive = false
	if err := (&nativeTunBindJournal{owner: j.owner, backend: j.owner.tunBinds, helperGroups: j.groups}).retire(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	fresh, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.confirmDeviceReceipt(t.Context(), fresh, []byte("{}")); err == nil {
		t.Fatal("replacement borrowed prior setup")
	}
	if _, err := j.owner.records(t.Context()); err != nil {
		t.Fatal(err)
	}
	backend.namespaceLive = true
	if _, err := j.owner.records(t.Context()); err == nil {
		t.Fatal("archived namespace reappearance bypassed inventory")
	}
}
