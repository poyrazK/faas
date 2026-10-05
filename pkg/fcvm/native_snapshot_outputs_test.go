//go:build linux || darwin

// adr: 568 — portable capture-output fixtures establish ownership, not native IO.
package fcvm

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nativeSnapshotOutputFixture struct {
	*nativeWritableImageFixture
	openOutput func(nativeImageSourceRecord, nativeImageReference, string) (*os.File, error)
	output     *os.File
	opens      int
}

func (b *nativeSnapshotOutputFixture) OpenSnapshotOutput(record nativeImageSourceRecord, ref nativeImageReference, point string) (*os.File, error) {
	b.opens++
	if err := errors.Join(b.CheckReference(record, ref), b.CheckAnchor(record, point)); err != nil {
		return nil, err
	}
	var err error
	if b.openOutput != nil {
		b.output, err = b.openOutput(record, ref, point)
	} else {
		b.output, err = os.OpenFile(point, os.O_RDONLY, 0)
	}
	return b.output, err
}

func (b *nativeSnapshotOutputFixture) PrepareSnapshotOutput(ctx context.Context, owner nativeLaunchRecord, root, directory, name string) (nativeImagePreparation, error) {
	if owner.Authorized || owner.PID != 0 || owner.StartTime != 0 {
		return nil, errors.New("fixture: live process fields leaked into image preparation")
	}
	return b.PrepareWritable(ctx, owner, root, directory, name)
}

type nativeCaptureOutputFixture struct {
	v         *JailerVMM
	q         *nativeQualificationJournal
	j         *nativeImageSourceJournal
	owner     nativeLaunchRecord
	incoming  nativeQualificationRecord
	capture   nativeQualificationCaptureRecord
	b         *nativeSnapshotOutputFixture
	root      string
	directory string
	ctx       context.Context
	lock      *os.File
}

func nativeCaptureOutputsFixture(t *testing.T) nativeCaptureOutputFixture {
	t.Helper()
	q, frame, ctx := nativeQualificationFixture(t)
	incoming, err := q.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.owner.prepare(nativeQualificationContext(ctx, incoming), qualificationLease(frame.InstanceID)); err != nil {
		t.Fatal(err)
	}
	incoming, err = q.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := q.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
	if err := q.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	capture := nativeQualificationCaptureRecord{Version: 1, InstanceID: frame.InstanceID, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, StartedAt: q.clock().UTC()}
	if err := q.writeCapture(incoming, capture); err != nil {
		t.Fatal(err)
	}
	b := &nativeSnapshotOutputFixture{nativeWritableImageFixture: &nativeWritableImageFixture{
		nativeImageBackendFixture: newNativeImageBackendFixture(), clones: make(map[nativeLoopIdentity]*nativeImageBackendFixture)}}
	q.owner.imageSources = b
	j := &nativeImageSourceJournal{owner: q.owner, backend: b}
	r := &nativeProcessRecoveryRuntime{journal: q.owner, imageSources: b, owned: make(map[string]string)}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	daemonLock := r.daemonLock
	t.Cleanup(func() {
		if err := daemonLock.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	r.remember(owner)
	base := t.TempDir()
	v := &JailerVMM{chrootBase: base, fcName: "firecracker", nativeRecovery: r}
	lock, err := q.lock(ctx, frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	return nativeCaptureOutputFixture{v: v, q: q, j: j, owner: owner, incoming: incoming, capture: capture, b: b,
		root: v.chrootRoot(frame.InstanceID), directory: t.TempDir(), ctx: nativeSnapshotCaptureContext(ctx, incoming, capture, owner), lock: lock}
}

func TestNativeSnapshotOutputsRetainDistinctOriginalEpochsThroughRetirement(t *testing.T) {
	f := nativeCaptureOutputsFixture(t)
	names, err := f.v.stageNativeSnapshotOutputs(f.ctx, f.owner.Lease, f.directory)
	if err != nil || names.Memory != "capture-"+f.capture.CaptureID+"-mem" || names.VMState != "capture-"+f.capture.CaptureID+"-vmstate" {
		t.Fatalf("original capture outputs: %+v %v", names, err)
	}
	records, err := f.j.records()
	if err != nil || len(records) != 2 || records[0].Epoch == records[1].Epoch || records[0].Identity == records[1].Identity {
		t.Fatalf("independent output epochs: %+v %v", records, err)
	}
	for _, record := range records {
		if len(record.References) != 1 {
			t.Fatalf("output inode has multiple owners: %+v", record)
		}
		ref := record.References[0]
		if !record.Ready || !ref.Ready || ref.Link || ref.ReadOnly || ref.AddPerms != 0 || !sameNativeImageOwner(ref, f.owner) ||
			ref.Owner.Authorized || ref.Owner.PID != 0 || ref.Owner.StartTime != 0 || ref.Root != f.root {
			t.Fatalf("output lacks original exclusive writable ownership: %+v", record)
		}
	}
	if f.b.prepares != 2 || f.b.closes != 2 {
		t.Fatalf("output producers escaped: prepares=%d closes=%d", f.b.prepares, f.b.closes)
	}
	if len(f.v.materialisedTmp) != 0 || len(f.v.bindMounts) != 0 || f.v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("output primitive borrowed legacy authority or enabled unfinished capture")
	}
	if err := f.j.require(f.ctx, f.owner, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.v.stageNativeSnapshotOutputs(f.ctx, f.owner.Lease, f.directory); err == nil || f.b.prepares != 2 {
		t.Fatal("original capture recreated its already-owned outputs", err)
	}
	retired := retireNativeImageFixtureOwner(t, f.j, f.owner)
	if err := f.q.owner.confirmResourcesRemoved(t.Context(), retired); err == nil {
		t.Fatal("retained capture outputs released physical ownership")
	}
	if err := f.j.retireAll(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if err := f.q.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if err := f.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.owner.records(t.Context()); err != nil {
		t.Fatal("output retirement broke restart inventory", err)
	}
}

func TestNativeSnapshotOutputsRefuseChangedCaptureBeforeProduction(t *testing.T) {
	for _, change := range []string{"boot_context", "no_context", "old_backend", "recovered", "daemon_generation", "no_daemon_lock", "closed_daemon_lock",
		"lease", "instance", "physical_generation", "physical_lease", "pid", "start_time", "prepared", "revoked", "exited", "removed", "builder",
		"incoming_generation", "incoming_frame", "incoming_revoked", "incoming_expired", "incoming_missing", "capture_missing", "capture_changed",
		"capture_completed", "permit_capture", "permit_physical", "relative_directory", "unclean_directory", "root_directory", "canceled"} {
		t.Run(change, func(t *testing.T) {
			f := nativeCaptureOutputsFixture(t)
			ctx, lease, directory := f.ctx, f.owner.Lease, f.directory
			physical, incoming, capture := f.owner, f.incoming, f.capture
			switch change {
			case "boot_context":
				ctx = nativeQualificationContext(t.Context(), incoming)
			case "no_context":
				ctx = t.Context()
			case "old_backend":
				f.v.nativeRecovery.imageSources = f.b.nativeImageBackendFixture
			case "recovered":
				delete(f.v.nativeRecovery.owned, lease.Instance)
			case "daemon_generation":
				f.v.nativeRecovery.owned[lease.Instance] = uuid.NewString()
			case "no_daemon_lock":
				f.v.nativeRecovery.daemonLock = nil
			case "closed_daemon_lock":
				if err := f.v.nativeRecovery.daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			case "lease":
				lease.MemoryMaxMiB++
			case "instance":
				lease.Instance = uuid.NewString()
			case "physical_generation":
				physical.Generation = uuid.NewString()
			case "physical_lease":
				physical.Lease.CPUMillicores++
			case "pid":
				physical.PID++
			case "start_time":
				physical.StartTime++
			case "prepared":
				physical.Authorized, physical.PID, physical.StartTime = false, 0, 0
			case "revoked":
				physical.Revoked = true
			case "exited":
				physical.Revoked, physical.ExitConfirmed = true, true
			case "removed":
				physical.Revoked, physical.ExitConfirmed, physical.ResourcesRemoved = true, true, true
			case "builder":
				physical.Lease.IsBuilder = true
			case "incoming_generation":
				incoming.Generation = uuid.NewString()
			case "incoming_frame":
				incoming.Execution.Attempt++
			case "incoming_revoked":
				incoming.Revoked = true
			case "incoming_expired":
				incoming.AcceptedAt, incoming.Deadline = time.Now().Add(-2*time.Second).UTC(), time.Now().Add(-time.Second).UTC()
			case "incoming_missing":
				path, err := f.q.path(lease.Instance)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "capture_missing":
				path, err := f.q.capturePath(lease.Instance)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "capture_changed":
				capture.StartedAt = capture.StartedAt.Add(time.Nanosecond)
			case "capture_completed":
				capture.CompletedAt, capture.Info = time.Now().UTC(), SnapshotInfo{MemBytes: 1, VMStateBytes: 1, StoredBytes: 1}
				capture.Backing = BackingIdentity{Version: 1, Kernel: "sha256:" + strings.Repeat("a", 64), Base: "sha256:" + strings.Repeat("b", 64)}
			case "permit_capture":
				changed := capture
				changed.CaptureID = uuid.NewString()
				ctx = nativeSnapshotCaptureContext(ctx, incoming, changed, physical)
			case "permit_physical":
				changed := physical
				changed.PID++
				ctx = nativeSnapshotCaptureContext(ctx, incoming, capture, changed)
			case "relative_directory":
				directory = "private-outputs"
			case "unclean_directory":
				directory += "/../outputs"
			case "root_directory":
				directory = "/"
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if physical != f.owner {
				if err := f.q.owner.write(physical); err != nil {
					t.Fatal(err)
				}
			}
			if incoming != f.incoming {
				if err := f.q.write(incoming); err != nil {
					t.Fatal(err)
				}
			}
			if capture != f.capture {
				if err := f.q.writeCapture(f.incoming, capture); err != nil {
					t.Fatal(err)
				}
			}
			if names, err := f.v.stageNativeSnapshotOutputs(ctx, lease, directory); err == nil || names != (nativeSnapshotOutputNames{}) || f.b.prepares != 0 {
				t.Fatalf("changed capture reached production: names=%+v prepares=%d error=%v", names, f.b.prepares, err)
			}
			if records, err := f.j.records(); err != nil || len(records) != 0 {
				t.Fatalf("rejected capture left epochs: %+v %v", records, err)
			}
		})
	}
}

func TestNativeSnapshotOutputInterruptedCheckpointRefusesReplacement(t *testing.T) {
	for _, checkpoint := range []int{1, 2, 3, 4, 5, 6} {
		t.Run(strconv.Itoa(checkpoint), func(t *testing.T) {
			f := nativeCaptureOutputsFixture(t)
			injected := errors.New("capture output acknowledgement lost")
			writes := 0
			f.j.writeValue = func(path string, record nativeImageSourceRecord) error {
				writes++
				if writes == checkpoint {
					// The checkpoint was durable but its caller cannot know that.
					return errors.Join(writeNativeJournalValue(path, record), injected)
				}
				return writeNativeJournalValue(path, record)
			}
			permit := f.ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
			if _, err := f.j.stageCaptureOutput(f.ctx, f.owner, permit, f.root, f.directory, "mem"); !errors.Is(err, injected) {
				t.Fatal("lost checkpoint did not fail capture", err)
			}
			if f.b.prepares != 1 || f.b.closes != 1 {
				t.Fatalf("uncertain producer escaped: prepares=%d closes=%d", f.b.prepares, f.b.closes)
			}
			f.j.writeValue = nil
			if _, err := f.j.stageCaptureOutput(f.ctx, f.owner, permit, f.root, f.directory, "mem"); err == nil || f.b.prepares != 1 {
				t.Fatal("uncertain capture replaced its original inode", err)
			}
			retired := retireNativeImageFixtureOwner(t, f.j, f.owner)
			if err := f.j.retireAll(t.Context(), retired); err != nil {
				t.Fatal("original output could not retire", err)
			}
			if err := f.q.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeSnapshotOutputsSecondProducerFailureRetainsFirstOutput(t *testing.T) {
	f := nativeCaptureOutputsFixture(t)
	injected := errors.New("second output failed")
	f.b.prepare = func(context.Context) error {
		if f.b.prepares == 2 {
			return injected
		}
		return nil
	}
	names, err := f.v.stageNativeSnapshotOutputs(f.ctx, f.owner.Lease, f.directory)
	if !errors.Is(err, injected) || names.Memory == "" || names.VMState != "" || f.b.prepares != 2 || f.b.closes != 1 {
		t.Fatalf("partial output preparation: %+v prepares=%d closes=%d error=%v", names, f.b.prepares, f.b.closes, err)
	}
	if records, err := f.j.records(); err != nil || len(records) != 1 || records[0].References[0].Name != names.Memory {
		t.Fatalf("first output lost original authority: %+v %v", records, err)
	}
	if _, err := f.v.stageNativeSnapshotOutputs(f.ctx, f.owner.Lease, f.directory); err == nil || f.b.prepares != 2 {
		t.Fatal("partial capture allowed a replacement producer", err)
	}
	retired := retireNativeImageFixtureOwner(t, f.j, f.owner)
	if err := f.j.retireAll(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if err := f.q.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSnapshotOutputProducerClosesBeforeOriginalRevocation(t *testing.T) {
	f := nativeCaptureOutputsFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f.b.prepare = func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	produced := make(chan error, 1)
	go func() {
		_, err := f.v.stageNativeSnapshotOutput(f.ctx, f.owner.Lease, f.directory, "mem")
		produced <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("output producer did not enter")
	}
	revoked := make(chan error, 1)
	go func() { _, err := f.q.owner.revoke(t.Context(), f.owner.Lease.Instance); revoked <- err }()
	select {
	case err := <-revoked:
		t.Fatal("physical revocation passed active output producer", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-produced; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if f.b.closes != 1 {
		t.Fatal("revocation acknowledged before producer descriptors closed")
	}
	if _, err := f.v.stageNativeSnapshotOutput(f.ctx, f.owner.Lease, f.directory, "vmstate"); err == nil || f.b.prepares != 1 {
		t.Fatal("revoked first output allowed a second producer", err)
	}
}

func TestNativeSnapshotOutputIncomingRevocationWaitsForCapture(t *testing.T) {
	f := nativeCaptureOutputsFixture(t)
	started, revoked := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := f.q.revoke(t.Context(), f.incoming.Execution)
		revoked <- err
	}()
	<-started
	select {
	case err := <-revoked:
		t.Fatal("incoming revocation passed original capture lock", err)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := f.v.stageNativeSnapshotOutputs(f.ctx, f.owner.Lease, f.directory); err != nil {
		t.Fatal("capture could not finish while retaining incoming authority", err)
	}
	if err := f.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if _, err := f.v.stageNativeSnapshotOutput(f.ctx, f.owner.Lease, f.directory, "mem"); err == nil || f.b.prepares != 2 {
		t.Fatal("revocation left output producer authority", err)
	}
}

func TestNativeSnapshotOutputCloseFailureRetainsRecoveryAuthority(t *testing.T) {
	f := nativeCaptureOutputsFixture(t)
	injected := errors.New("output descriptor close acknowledgement lost")
	f.b.closeInput = func() error {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		if _, err := f.q.owner.revoke(ctx, f.owner.Lease.Instance); !errors.Is(err, context.DeadlineExceeded) {
			t.Error("physical ownership released before producer close", err)
		}
		return injected
	}
	if _, err := f.v.stageNativeSnapshotOutput(f.ctx, f.owner.Lease, f.directory, "mem"); !errors.Is(err, injected) {
		t.Fatal("uncertain descriptor close acknowledged capture", err)
	}
	f.b.closeInput = nil
	if _, err := f.v.stageNativeSnapshotOutput(f.ctx, f.owner.Lease, f.directory, "mem"); err == nil || f.b.prepares != 1 {
		t.Fatal("close uncertainty replaced original output", err)
	}
	retired := retireNativeImageFixtureOwner(t, f.j, f.owner)
	if err := f.j.retireAll(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if err := f.q.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSnapshotOutputNamesRequireCanonicalCaptureAndKnownKind(t *testing.T) {
	for _, capture := range []string{"", "../foreign", "D3124D9A-657B-4F37-A9DF-F5DFB2E50764", "00000000-0000-0000-0000-000000000000"} {
		if _, err := nativeSnapshotOutputName(capture, "mem"); err == nil {
			t.Fatalf("invalid capture gained an output name: %q", capture)
		}
	}
	for _, kind := range []string{"", "memory", "../vmstate", "layer.ext4"} {
		if _, err := nativeSnapshotOutputName(uuid.NewString(), kind); err == nil {
			t.Fatalf("unowned output kind gained a name: %q", kind)
		}
	}
}
