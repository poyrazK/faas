package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

type nativeJailDeviceFrame struct {
	Scope        jailsetup.DeviceSetupScope    `json:"scope"`
	InputsClosed bool                          `json:"inputs_closed"`
	Receipt      *jailsetup.DeviceSetupReceipt `json:"receipt,omitempty"`
}

func (f *nativeJailDeviceFrame) UnmarshalJSON(data []byte) error {
	_, err := nativeJournalObjectFields(data, []string{"scope", "inputs_closed"})
	if err != nil {
		if _, err := nativeJournalObjectFields(data, []string{"scope", "inputs_closed", "receipt"}); err != nil {
			return err
		}
	}
	type plain nativeJailDeviceFrame
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(f))
}

type nativeJailDeviceInputs interface {
	Scope() jailsetup.DeviceSetupScope
	Files() []*os.File
	Close() error
}
type nativeJailDeviceBackend interface {
	Prepare(context.Context, nativeLaunchRecord, string, nativeTunSource) (nativeJailDeviceInputs, error)
	InputsRemoved(context.Context, jailsetup.DeviceSetupScope) error
	NamespaceRemoved(context.Context, jailsetup.DeviceSetupScope) error
}

func newNativeJailDeviceCommand(helper string) (*exec.Cmd, error) {
	command, err := newNativeHostHelperCommand(helper, []string{helper})
	if err != nil {
		return nil, err
	}
	command.Args = []string{"vmmd-host-helper", "--launch-jail-device-setup", "3"}
	return command, nil
}

func (j *nativeHostHelperJournal) prepareDeviceInputs(ctx context.Context, owner nativeLaunchRecord) (nativeJailDeviceInputs, error) {
	if j.owner.jailDevices == nil {
		return nil, errors.New("native jail device setup: native handoff backend unavailable")
	}
	records, err := j.records(owner)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.JailDevice != nil {
			return nil, errors.New("native jail device setup: original generation already retains a setup frame")
		}
	}
	tun := nativeTunBindJournal{owner: j.owner, backend: j.owner.tunBinds}
	bindings, err := tun.records()
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if sameNativeTunOwner(binding, owner) && binding.Ready && !binding.Removed {
			if binding.Root != j.deviceRoot {
				return nil, errors.New("native jail device setup: original root differs from host TUN authority")
			}
			return j.owner.jailDevices.Prepare(ctx, owner, j.deviceRoot, binding.Source)
		}
	}
	return nil, errors.New("native jail device setup: original host TUN binding is absent")
}

func (j *nativeHostHelperJournal) confirmDeviceReceipt(ctx context.Context, expected nativeLaunchRecord, data []byte) (err error) {
	var receipt jailsetup.DeviceSetupReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	lock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := j.owner.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if owner.Generation != expected.Generation || owner.KernelBootID != expected.KernelBootID || !sameNativePhysicalLease(owner.Lease, expected.Lease) || owner.Revoked || !owner.Authorized || owner.ResourcesRemoved {
		return errors.New("native jail device receipt: original VM authority changed")
	}
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.JailDevice == nil {
			continue
		}
		if !record.JailDevice.InputsClosed || !record.Launch.Authorized || !record.Launch.ResourcesRemoved {
			return errors.New("native jail device receipt: producer retirement is incomplete")
		}
		if j.groups == nil {
			return errors.New("native jail device receipt: helper retirement backend unavailable")
		}
		if err := j.groups.Removed(record.Group); err != nil {
			return err
		}
		if err := receipt.Validate(record.JailDevice.Scope); err != nil {
			return err
		}
		record.JailDevice.Receipt = &receipt
		return j.write(owner, record)
	}
	return errors.New("native jail device receipt: original producer frame is absent")
}

func (j *nativeHostHelperJournal) requireDeviceReady(owner nativeLaunchRecord) error {
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.JailDevice != nil && (record.JailDevice.Receipt == nil || !record.JailDevice.InputsClosed || !record.Launch.ResourcesRemoved) {
			return errors.New("native jail device setup: original device readiness remains uncertain")
		}
	}
	return nil
}

func (j *nativeHostHelperJournal) requireDeviceNamespacesRemoved(ctx context.Context, owner nativeLaunchRecord) error {
	records, err := j.records(owner)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.JailDevice == nil {
			continue
		}
		if !record.Launch.ResourcesRemoved || !record.JailDevice.InputsClosed || j.owner.jailDevices == nil {
			return errors.New("native jail device cleanup: original producer has no descriptor retirement proof")
		}
		if err := j.owner.jailDevices.NamespaceRemoved(ctx, record.JailDevice.Scope); err != nil {
			return err
		}
	}
	return nil
}

func (v *JailerVMM) bindNativeJailDevices(ctx context.Context, expected nativeLaunchRecord, root, instance string, uid, gid int) (timings bindTunTimings, result error) {
	started := time.Now()
	defer func() { timings.SetupJailMs = time.Since(started).Milliseconds() }()
	r := v.nativeRecovery
	if r == nil || expected.Generation != r.generation(instance) || expected.Lease.Instance != instance || expected.Lease.UID != uid || expected.Lease.GID != gid || root != v.chrootRoot(instance) {
		return timings, errors.New("native jail device setup: original local lease authority is unavailable")
	}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		return timings, err
	}
	helper := r.helper
	var err error
	if helper == "" {
		helper, err = v.ensureMountHelper()
		if err != nil {
			return timings, err
		}
	}
	command, err := newNativeJailDeviceCommand(helper)
	if err != nil {
		return timings, err
	}
	var output nativeHostHelperOutput
	command.Stdout, command.Stderr = &output, &output
	j := nativeHostHelperJournal{owner: r.journal, groups: r.helperGroups, startTime: r.startTime, purpose: nativeHostHelperJailDevices, deviceRoot: root}
	if err := j.run(ctx, expected, command, v.nativeCleanupBudget()); err != nil {
		return timings, err
	}
	return timings, j.confirmDeviceReceipt(ctx, expected, output.Bytes())
}

func (v *JailerVMM) nativeCleanupBudget() time.Duration {
	if v.destroyWait > 0 {
		return v.destroyWait
	}
	return 10 * time.Minute
}
