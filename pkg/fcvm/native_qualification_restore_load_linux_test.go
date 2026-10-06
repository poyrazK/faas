//go:build linux

// Modeled native mounts/process authority, real pinned descriptor hashing.
// Only TestMetalNativeCaptureVM supplies Firecracker load acceptance.
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type nativeLoadSequenceReceipts struct {
	*nativePublicationIntentFixture
}

func (j nativeLoadSequenceReceipts) ReadRestoreCohort(ctx context.Context, capture string) (r nativeSnapshotRestoreCohort, err error) {
	if capture != j.intent.Capture.CaptureID {
		return r, errors.New("original capture changed")
	}
	r.Intent = j.intent
	for i, kind := range []string{"mem", "vmstate", "drive", "backing"} {
		r.Objects[i] = j.objects[kind]
	}
	return r, ctx.Err()
}

type nativeLoadSequenceImages struct {
	*nativeRestoreBackingFixtureBackend
	paths  map[string]string
	opened []*os.File
}

func (b *nativeLoadSequenceImages) OpenRestoreImage(_ nativeImageSourceRecord, ref nativeImageReference, _ string, _ *nativeSnapshotBackingImage) (*os.File, error) {
	f, err := os.Open(b.paths[ref.Name])
	if err == nil {
		b.opened = append(b.opened, f)
	}
	return f, err
}

type nativeLoadSequenceControl struct{ f *nativeLoadSequenceFixture }

func (b nativeLoadSequenceControl) Request(ctx context.Context, socket string, owner nativeLaunchRecord, method, path string, body any) error {
	f := b.f
	if owner != f.owner || socket != f.v.socketPath(owner.Lease.Instance) {
		return errors.New("target control peer changed")
	}
	r, err := f.loads.read(owner.Lease.Instance)
	if err != nil {
		return err
	}
	f.calls++
	if f.calls == 1 {
		encoded, _ := json.Marshal(body)
		if method != http.MethodPut || path != "/snapshot/load" || string(encoded) != `{"mem_backend":{"backend_path":"snap-in-mem","backend_type":"File"},"resume_vm":false,"snapshot_path":"snap-in-vmstate"}` || r.Phases[0].IsZero() || !r.Phases[1].IsZero() {
			return errors.New("load effect preceded original paused-load intent")
		}
	} else if f.calls == 2 {
		encoded, _ := json.Marshal(body)
		if method != http.MethodPatch || path != "/vm" || string(encoded) != `{"state":"Resumed"}` || r.Phases[2].IsZero() || !r.Phases[3].IsZero() {
			return errors.New("resume preceded original intent")
		}
	} else {
		return errors.New("uncertain control was replayed")
	}
	if f.afterControl != nil {
		return f.afterControl(f.calls)
	}
	return ctx.Err()
}

type nativeLoadSequenceResume struct{ f *nativeLoadSequenceFixture }

func (b nativeLoadSequenceResume) Resume(ctx context.Context, _ *JailerVMM, owner nativeLaunchRecord) error {
	f := b.f
	r, err := f.loads.read(owner.Lease.Instance)
	if err != nil {
		return err
	}
	f.hooks++
	if f.calls != 2 || f.hooks != 1 || r.Phases[4].IsZero() || !r.Phases[5].IsZero() {
		return errors.New("hook preceded original resume acknowledgement or replayed")
	}
	return errors.Join(f.hookErr, ctx.Err())
}

type nativeLoadSequenceFixture struct {
	v            *JailerVMM
	q            *nativeQualificationJournal
	target       nativeQualificationRestoreRecord
	owner        nativeLaunchRecord
	loads        *nativeQualificationRestoreLoadJournal
	b            *nativeLoadSequenceImages
	ctx          context.Context
	calls, hooks int
	afterControl func(int) error
	hookErr      error
	fences       []*nativeLoadSequenceFence
	fenceChanged bool
}

type nativeLoadSequenceFenceBackend struct{ f *nativeLoadSequenceFixture }
type nativeLoadSequenceFence struct {
	f      *nativeLoadSequenceFixture
	group  nativeHostHelperGroup
	closed bool
}

func (b nativeLoadSequenceFenceBackend) Pin(ctx context.Context, owner nativeLaunchRecord) (nativeQualificationRestoreFence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fence := &nativeLoadSequenceFence{f: b.f, group: nativeHostHelperGroup{Path: nativeSnapshotMemoryPath(owner), Device: 50, Inode: 51}}
	b.f.fences = append(b.f.fences, fence)
	return fence, ctx.Err()
}
func (f *nativeLoadSequenceFence) Group() nativeHostHelperGroup { return f.group }
func (f *nativeLoadSequenceFence) Require(ctx context.Context) error {
	if f.closed || f.f.fenceChanged {
		return errors.New("original cgroup or limit changed")
	}
	return ctx.Err()
}
func (f *nativeLoadSequenceFence) Close() error { f.closed = true; return nil }

func nativeRestoreLoadSequenceFixture(t *testing.T) *nativeLoadSequenceFixture {
	t.Helper()
	f, backings, target, owner, b, paths, ctx := nativeRestoreBackingsFixture(t)
	v, q := f.v, f.q
	if err := v.stageNativeQualificationRestoreBackings(ctx, owner, paths); err != nil {
		t.Fatal(err)
	}
	images := nativeLoadSequenceImages{nativeRestoreBackingFixtureBackend: b, paths: map[string]string{}}
	v.nativeRecovery.imageSources, q.owner.imageSources = &images, &images
	j := nativeImageSourceJournal{owner: q.owner, backend: &images}
	for _, name := range []string{memSnapshotName, vmstateSnapshotName, layerImageName} {
		readOnly, perms := name != layerImageName, uint32(0)
		if readOnly {
			perms = 0o044
		}
		if _, err := j.stagePrepared(ctx, owner, v.chrootRoot(owner.Lease.Instance), name, readOnly, perms, func(original nativeLaunchRecord) (nativeImagePreparation, error) {
			return b.PrepareWritable(ctx, original, v.chrootRoot(original.Lease.Instance), "", name)
		}); err != nil {
			t.Fatal(err)
		}
	}
	completed, err := q.readCapture(f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	receipts := nativeLoadSequenceReceipts{nativePublicationIntentFixture: &nativePublicationIntentFixture{}}
	intent, err := receipts.Begin(ctx, nativeSnapshotPublicationIntent{Version: 1, JailBase: v.chrootBase, Incoming: f.incoming, Capture: backings.Capture, Physical: f.owner, Keys: nativeSnapshotIntentKeys(f.incoming)})
	if err != nil {
		t.Fatal(err)
	}
	completed.Info = SnapshotInfo{}
	sources, err := j.records()
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{backings.Images[0].Name, backings.Images[1].Name, memSnapshotName, vmstateSnapshotName, layerImageName} {
		path := filepath.Join(f.directory, "load-candidate-"+name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			_, err = file.Write([]byte("captured-backing-" + name))
		} else if i == 2 {
			err = file.Truncate(int64(target.Execution.RAMMB) << 20)
		} else {
			_, err = file.Write([]byte("captured-" + name))
		}
		if err != nil {
			t.Fatal(err)
		}
		stat, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		identity, _, err := nativeImageFileMetadata(file)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.New()
		if _, err := io.Copy(digest, io.NewSectionReader(file, 0, stat.Size())); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		images.paths[name] = path
		for _, source := range sources {
			if len(source.References) != 1 || !sameNativeImageOwner(source.References[0], owner) || source.References[0].Name != name {
				continue
			}
			old := source
			images.clones[identity] = images.clones[source.Identity]
			source.Identity = identity
			if err := q.owner.imageSources.(*nativeLoadSequenceImages).CheckReference(old, old.References[0]); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(j.path(old)); err != nil {
				t.Fatal(err)
			}
			if err := j.write(source); err != nil {
				t.Fatal(err)
			}
		}
		if i >= 2 {
			kind := []string{"mem", "vmstate", "drive"}[i-2]
			object := nativeModeledArtifactReceipt(nativePublicationObjectKey(intent, kind), []byte("modeled"))
			object.LogicalBytes, object.StoredBytes, object.SHA256 = stat.Size(), stat.Size(), hex.EncodeToString(digest.Sum(nil))
			if _, err := receipts.RecordObject(ctx, intent, kind, object); err != nil {
				t.Fatal(err)
			}
			completed.Info.StoredBytes += object.StoredBytes
			if i == 2 {
				completed.Info.MemBytes = stat.Size()
			}
			if i == 3 {
				completed.Info.VMStateBytes = stat.Size()
			}
		}
	}
	body, err := json.Marshal(completed.Backing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receipts.RecordObject(ctx, intent, "backing", nativeModeledArtifactReceipt(intent.Keys.Backing, body)); err != nil {
		t.Fatal(err)
	}
	if err := q.writeCapture(f.incoming, completed); err != nil {
		t.Fatal(err)
	}
	target, err = q.restores().read(target.Execution.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	target.Capture = qualificationSnapshotProof(f.incoming, completed.Info)
	if err := q.restores().write(target); err != nil {
		t.Fatal(err)
	}
	owner.Authorized, owner.PID, owner.StartTime = true, 4242, 1001
	if err := q.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	result := &nativeLoadSequenceFixture{v: v, q: q, target: target, owner: owner, loads: q.restores().loads(), b: &images, ctx: nativeQualificationRestoreContext(ctx, target)}
	v.nativeRecovery.snapshotControl = nativeLoadSequenceControl{f: result}
	v.nativeRecovery.restoreResume = nativeLoadSequenceResume{f: result}
	v.nativeRecovery.restoreFence = nativeLoadSequenceFenceBackend{f: result}
	v.nativeRecovery.publications = receipts
	return result
}

func TestNativeQualificationRestoreLoadSequenceRetainsOriginalIntentAndCannotReplay(t *testing.T) {
	for _, outcome := range []string{"success", "load_lost", "missing_load_journal", "resume_lost", "hook_lost", "cancel_after_load", "changed_process", "changed_image", "changed_fence"} {
		t.Run(outcome, func(t *testing.T) {
			f := nativeRestoreLoadSequenceFixture(t)
			lost := errors.New("effect acknowledgement lost")
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			f.afterControl = func(call int) error {
				if (outcome == "load_lost" || outcome == "missing_load_journal") && call == 1 || outcome == "resume_lost" && call == 2 {
					return lost
				}
				if call == 1 {
					switch outcome {
					case "changed_fence":
						f.fenceChanged = true
					case "cancel_after_load":
						cancel()
					case "changed_process":
						physical := f.owner
						physical.StartTime++
						return f.q.owner.write(physical)
					case "changed_image":
						sources, err := (&nativeImageSourceJournal{owner: f.q.owner}).records()
						if err != nil {
							return err
						}
						for _, s := range sources {
							if sameNativeImageOwner(s.References[0], f.owner) && s.References[0].Name == memSnapshotName {
								s.References[0].Ready = false
								return (&nativeImageSourceJournal{owner: f.q.owner}).write(s)
							}
						}
					}
				}
				return nil
			}
			if outcome == "hook_lost" {
				f.hookErr = lost
			}
			r, err := f.v.loadNativeQualificationRestore(ctx, f.owner.Lease)
			if (err == nil) != (outcome == "success") {
				t.Fatal(outcome, err)
			}
			stored, readErr := f.loads.read(f.owner.Lease.Instance)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if outcome == "success" && (r != stored || stored.Phases[5].IsZero() || f.calls != 2 || f.hooks != 1) {
				t.Fatal("complete native effects lack exact original evidence")
			}
			calls, hooks := f.calls, f.hooks
			if outcome == "missing_load_journal" {
				path, _ := f.loads.path(f.owner.Lease.Instance)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				f.ctx = nativeQualificationRestoreContext(f.ctx, f.target)
			}
			if _, err := f.v.loadNativeQualificationRestore(f.ctx, f.owner.Lease); err == nil || f.calls != calls || f.hooks != hooks {
				t.Fatal("uncertain or completed effect was replayed", err)
			}
			for _, file := range f.b.opened {
				if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("load input descriptor escaped its source lock", err)
				}
			}
			for _, fence := range f.fences {
				if !fence.closed {
					t.Fatal("original cgroup capability escaped the physical lock")
				}
			}
			if outcome == "success" {
				if err := f.loads.validateInventory(t.Context()); err != nil {
					t.Fatal("recovery lost retained load evidence", err)
				}
			}
		})
	}
}

func TestNativeQualificationRestoreLoadLostJournalAcknowledgementsStopBeforeNextEffect(t *testing.T) {
	for faultStep := 0; faultStep < nativeRestorePhaseCount; faultStep++ {
		t.Run([]string{"load_intent", "load_ack", "resume_intent", "resume_ack", "hook_intent", "hook_ack"}[faultStep], func(t *testing.T) {
			f := nativeRestoreLoadSequenceFixture(t)
			lost := errors.New("journal acknowledgement lost")
			f.v.nativeRecovery.restoreLoadWrite = func(path string, r nativeQualificationRestoreLoadRecord) error {
				err := writeNativeJournalValue(path, r)
				last := 0
				for i, phase := range r.Phases {
					if !phase.IsZero() {
						last = i
					}
				}
				if last == faultStep {
					err = errors.Join(err, lost)
				}
				return err
			}
			if _, err := f.v.loadNativeQualificationRestore(f.ctx, f.owner.Lease); !errors.Is(err, lost) {
				t.Fatal(err)
			}
			calls, hooks := f.calls, f.hooks
			f.v.nativeRecovery.restoreLoadWrite = nil
			if _, err := f.v.loadNativeQualificationRestore(f.ctx, f.owner.Lease); err == nil || f.calls != calls || f.hooks != hooks {
				t.Fatal("lost journal acknowledgement replayed an effect", err)
			}
			wantCalls := []int{0, 1, 1, 2, 2, 2}[faultStep]
			wantHooks := 0
			if faultStep == 5 {
				wantHooks = 1
			}
			if calls != wantCalls || hooks != wantHooks {
				t.Fatal("effect escaped original durable intent", calls, hooks)
			}
		})
	}
}

func TestNativeQualificationRestoreLoadRefusesUnavailableAuthorityBeforeEffects(t *testing.T) {
	for _, change := range []string{"generic", "recovered", "revoked", "prepared", "missing_images", "content_changed", "ram", "capture_changed", "missing_control", "missing_hook", "missing_fence", "changed_fence", "canceled"} {
		t.Run(change, func(t *testing.T) {
			f := nativeRestoreLoadSequenceFixture(t)
			ctx := f.ctx
			switch change {
			case "generic":
				ctx = t.Context()
			case "recovered":
				delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
			case "revoked":
				if _, err := f.q.restores().revoke(t.Context(), f.target.Execution); err != nil {
					t.Fatal(err)
				}
			case "prepared":
				physical := f.owner
				physical.Authorized, physical.PID, physical.StartTime = false, 0, 0
				if err := f.q.owner.write(physical); err != nil {
					t.Fatal(err)
				}
			case "missing_images":
				sources, err := (&nativeImageSourceJournal{owner: f.q.owner}).records()
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range sources {
					if sameNativeImageOwner(s.References[0], f.owner) && s.References[0].Name == vmstateSnapshotName {
						if err := os.Remove((&nativeImageSourceJournal{owner: f.q.owner}).path(s)); err != nil {
							t.Fatal(err)
						}
					}
				}
			case "content_changed":
				body, err := os.ReadFile(f.b.paths[vmstateSnapshotName])
				if err != nil {
					t.Fatal(err)
				}
				body[0] ^= 1
				if err := os.WriteFile(f.b.paths[vmstateSnapshotName], body, 0o600); err != nil {
					t.Fatal(err)
				}
			case "ram":
				physical := f.owner
				physical.Lease.MemoryMaxMiB++
				if err := f.q.owner.write(physical); err != nil {
					t.Fatal(err)
				}
			case "capture_changed":
				source, err := f.q.read(f.target.Execution.CaptureInstanceID)
				if err != nil {
					t.Fatal(err)
				}
				capture, err := f.q.readCapture(source)
				if err != nil {
					t.Fatal(err)
				}
				capture.Info.StoredBytes++
				if err := f.q.writeCapture(source, capture); err != nil {
					t.Fatal(err)
				}
			case "missing_control":
				f.v.nativeRecovery.snapshotControl = nil
			case "missing_hook":
				f.v.nativeRecovery.restoreResume = nil
			case "missing_fence":
				f.v.nativeRecovery.restoreFence = nil
			case "changed_fence":
				f.fenceChanged = true
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := f.v.loadNativeQualificationRestore(ctx, f.owner.Lease); err == nil || f.calls != 0 || f.hooks != 0 {
				t.Fatal("unavailable authority reached native effect", err)
			}
			path, _ := f.loads.path(f.owner.Lease.Instance)
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unverified inputs published load intent", err)
			}
		})
	}
}
