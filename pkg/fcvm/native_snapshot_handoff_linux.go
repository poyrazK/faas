//go:build linux

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/jailsetup"
	"golang.org/x/sys/unix"
)

func (linuxNativeImageSources) CheckSnapshotOutputHandoff(ctx context.Context, v *JailerVMM) error {
	if v.nativeRecovery == nil || v.nativeRecovery.helperGroups == nil {
		return errors.New("native snapshot handoff: original helper adapter unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.nativeRecovery.helper == "" {
		_, err := v.ensureMountHelper()
		return err
	}
	_, err := resolveMountHelper(v.nativeRecovery.helper)
	return err
}

// Called with the original physical lock held, before pause. Both output epoch
// locks and read-only anchor FDs span gated launch, namespace effects, join,
// receipt acknowledgement and input closure. The helper never gets host names.
func (b linuxNativeImageSources) HandoffSnapshotOutputs(ctx context.Context, v *JailerVMM, owner nativeLaunchRecord) (result error) {
	permit, _, err := nativeSnapshotPublicationKeys(ctx, owner.Lease)
	if err != nil {
		return err
	}
	r := v.nativeRecovery
	if r == nil || r.helperGroups == nil || r.generation(owner.Lease.Instance) != owner.Generation {
		return errors.New("native snapshot handoff: original helper owner unavailable")
	}
	images := &nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	handoff := nativeSnapshotHandoffImageSources{linuxNativeImageSources: b}
	for i, kind := range []string{"mem", "vmstate"} {
		name, _ := nativeSnapshotOutputName(permit.Capture.CaptureID, kind)
		handoff.outputs[i], _, err = images.captureOutput(owner, v.chrootRoot(owner.Lease.Instance), name)
		if err != nil {
			return err
		}
	}
	// Verify the complete image cohort before taking the output locks. The
	// joined helper checks those same retained epochs without reacquiring them.
	cohort := *images
	cohort.backend = handoff
	if err := cohort.require(ctx, owner, false); err != nil {
		return err
	}
	j := &nativeHostHelperJournal{owner: r.journal, groups: r.helperGroups, startTime: r.startTime, purpose: nativeHostHelperSnapshotOutputs, deviceRoot: v.chrootRoot(owner.Lease.Instance), lockedOwner: &owner, snapshotPermit: &permit}
	var claims [2]nativeDiskImageClaim
	for i, kind := range []string{"mem", "vmstate"} {
		name, _ := nativeSnapshotOutputName(permit.Capture.CaptureID, kind)
		record, ref, err := images.captureOutput(owner, j.deviceRoot, name)
		if err != nil {
			return err
		}
		if i == 1 && record.Identity == j.snapshotRecords[0].Identity {
			return errors.New("native snapshot handoff: output aliases another epoch")
		}
		lock, err := images.lock(ctx, record.Identity)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, lock.Close()) }()
		current, currentRef, err := images.captureOutput(owner, j.deviceRoot, name)
		if err != nil || current.Epoch != record.Epoch || current.Identity != record.Identity || currentRef != ref {
			return errors.Join(err, errors.New("native snapshot handoff: output epoch changed"))
		}
		claim, err := b.diskStagingClaim(current, images.anchor(current))
		if err != nil || claim == nil {
			return errors.Join(err, errors.New("native snapshot handoff: original source lost its persistent link claim"))
		}
		claims[i] = *claim
		file, err := b.openSnapshotOutput(current, currentRef, images.anchor(current), true)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, file.Close()) }()
		j.snapshotFiles[i], j.snapshotRecords[i], j.snapshotRefs[i] = file, current, currentRef
	}
	if j.snapshotRecords[0].Identity == j.snapshotRecords[1].Identity {
		return errors.New("native snapshot handoff: output aliases another epoch")
	}
	if err := images.captureOutputAuthority(ctx, owner, owner, permit); err != nil {
		return err
	}
	helper := r.helper
	if helper == "" {
		helper, err = v.ensureMountHelper()
		if err != nil {
			return err
		}
	}
	command, err := newNativeSnapshotOutputCommand(helper)
	if err != nil {
		return err
	}
	var output nativeHostHelperOutput
	command.Stdout, command.Stderr = &output, &output
	if err := j.run(ctx, owner, command, v.nativeCleanupBudget()); err != nil {
		return fmt.Errorf("native snapshot handoff: original helper failed: %w (%s)", err, strings.TrimSpace(string(output.Bytes())))
	}
	if err := errors.Join(j.confirmSnapshotOutputReceipt(ctx, owner, output.Bytes()), r.checkDaemonOwnership(), ctx.Err()); err != nil {
		return err
	}
	// The kernel cannot attach an unlinked mount root. Keep only the original
	// immutable link claims through joined handoff, then remove their names
	// before pause while both output epochs and physical ownership are held.
	for _, claim := range claims {
		if err := retireNativeDiskImageClaim(b.diskStagingRoot, claim); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Only these two exact original capture records may retain connected dentries.
// Every other source in the cohort keeps the ordinary retired-link contract.
type nativeSnapshotHandoffImageSources struct {
	linuxNativeImageSources
	outputs [2]nativeImageSourceRecord
}

func (b nativeSnapshotHandoffImageSources) CheckAnchor(record nativeImageSourceRecord, point string) error {
	for _, output := range b.outputs {
		if reflect.DeepEqual(record, output) {
			return b.checkAnchor(record, point, true)
		}
	}
	return b.linuxNativeImageSources.CheckAnchor(record, point)
}

func (j *nativeHostHelperJournal) prepareSnapshotOutputInputs(ctx context.Context, owner nativeLaunchRecord) (p *nativeSnapshotOutputInputs, result error) {
	if j.snapshotPermit == nil || j.lockedOwner == nil || !sameNativeSnapshotProcess(*j.lockedOwner, owner) {
		return nil, errors.New("native snapshot handoff: original held capture owner required")
	}
	frames, err := j.records(owner)
	if err != nil {
		return nil, err
	}
	for _, frame := range frames {
		if frame.SnapshotOutput != nil {
			return nil, errors.New("native snapshot handoff: original generation already retains a producer")
		}
	}
	images := nativeImageSourceJournal{owner: j.owner, backend: j.owner.imageSources}
	if err := images.captureOutputAuthority(ctx, owner, owner, *j.snapshotPermit); err != nil {
		return nil, err
	}
	for i, kind := range []string{"mem", "vmstate"} {
		name, _ := nativeSnapshotOutputName(j.snapshotPermit.Capture.CaptureID, kind)
		record, ref, err := images.captureOutput(owner, j.deviceRoot, name)
		if err != nil || !reflect.DeepEqual(record, j.snapshotRecords[i]) || ref != j.snapshotRefs[i] {
			return nil, errors.Join(err, errors.New("native snapshot handoff: retained output epoch changed"))
		}
		backend, ok := j.owner.imageSources.(linuxNativeImageSources)
		if !ok {
			return nil, errors.New("native snapshot handoff: original Linux output adapter changed")
		}
		if err := errors.Join(backend.checkAnchor(record, images.anchor(record), true), backend.CheckReference(record, ref)); err != nil {
			return nil, err
		}
	}
	p = &nativeSnapshotOutputInputs{}
	defer func() {
		if result != nil {
			result = errors.Join(result, p.Close())
			p = nil
		}
	}()
	process, err := openNativeProcess(owner.PID)
	if err != nil {
		return p, err
	}
	// p owns this original process handle after successful open.
	p.files = []*os.File{nil, nil, os.NewFile(uintptr(process.(*nativePIDFD).fd), "original-snapshot-process")}
	if err := checkNativeSnapshotProcess(process.(*nativePIDFD), owner); err != nil {
		return p, err
	}
	root, err := os.OpenFile("/proc/"+strconv.Itoa(owner.PID)+"/root", os.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return p, err
	}
	p.files[0] = root
	actual, err := root.Stat()
	expected, expectedErr := os.Stat(j.deviceRoot)
	if err != nil || expectedErr != nil || !os.SameFile(actual, expected) {
		return p, errors.Join(err, expectedErr, errors.New("native snapshot handoff: original process root changed"))
	}
	namespace, err := os.OpenFile("/proc/"+strconv.Itoa(owner.PID)+"/ns/mnt", os.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return p, err
	}
	p.files[1] = namespace
	s := jailsetup.SnapshotOutputScope{Generation: owner.Generation, BootID: owner.KernelBootID, CaptureID: j.snapshotPermit.Capture.CaptureID, PID: owner.PID, StartTime: owner.StartTime, UID: owner.Lease.UID, GID: owner.Lease.GID, ParentPID: os.Getpid()}
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return p, err
	}
	s.ParentStartTime, err = nativeProcessStartTime(string(data), os.Getpid())
	if err != nil {
		return p, err
	}
	s.RootMountID, err = nativeDeviceFileMountID(root)
	if err != nil {
		return p, err
	}
	for i, source := range j.snapshotFiles {
		if source == nil {
			return p, errors.New("native snapshot handoff: missing locked original source")
		}
		fd, err := unix.FcntlInt(source.Fd(), unix.F_DUPFD_CLOEXEC, 3)
		if err != nil {
			return p, err
		}
		p.files = append(p.files, os.NewFile(uintptr(fd), "original-snapshot-output"))
		s.Epochs[i] = j.snapshotRecords[i].Epoch
		s.Targets[i] = jailsetup.DeviceFDIdentity{Device: j.snapshotRefs[i].Target.Device, Inode: j.snapshotRefs[i].Target.Inode}
	}
	identities := []*jailsetup.DeviceFDIdentity{&s.Root, &s.Namespace, &s.PIDHandle, &s.Outputs[0], &s.Outputs[1]}
	for i, file := range p.files {
		*identities[i], err = nativeDeviceFileIdentity(file)
		if err != nil {
			return p, err
		}
		s.ParentFDs[i] = int(file.Fd())
	}
	host, err := nativeLoopNamespaceIdentity()
	if err != nil || s.Namespace.Inode == host.Inode && s.Namespace.Device == host.Device {
		return p, errors.Join(err, errors.New("native snapshot handoff: original VM has no private namespace"))
	}
	for i := 0; i < 2; i++ {
		if s.Outputs[i].Device != j.snapshotRecords[i].Identity.Device || s.Outputs[i].Inode != j.snapshotRecords[i].Identity.Inode {
			return p, errors.New("native snapshot handoff: original output identity changed")
		}
	}
	p.scope = s
	return p, errors.Join(s.Validate(), checkNativeSnapshotProcess(process.(*nativePIDFD), owner), ctx.Err())
}

func nativeSnapshotOutputInputsRemoved(ctx context.Context, s jailsetup.SnapshotOutputScope) (result error) {
	if err := s.Validate(); err != nil {
		return err
	}
	fd, err := unix.PidfdOpen(s.ParentPID, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, unix.Close(fd)) }()
	proc, err := os.OpenFile("/proc/"+strconv.Itoa(s.ParentPID), unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, proc.Close()) }()
	start, err := nativeDeviceProcStart(proc, s.ParentPID)
	if err != nil {
		return err
	}
	if start != s.ParentStartTime {
		return nil
	}
	for i, number := range s.ParentFDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		var stat unix.Stat_t
		err := unix.Fstatat(int(proc.Fd()), "fd/"+strconv.Itoa(number), &stat, 0)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		if (jailsetup.DeviceFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) == s.Identities()[i] {
			return errors.New("native snapshot handoff: original parent input remains open")
		}
	}
	return nil
}
