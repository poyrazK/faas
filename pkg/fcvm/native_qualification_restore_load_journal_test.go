//go:build linux || darwin

// Modeled evidence tests grant no native process or guest-readiness authority.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeQualificationRestoreLoadCapabilityCannotBeRecreatedWithinOriginalContext(t *testing.T) {
	_, _, target, _, _, _ := nativeRestoreLoadRecordFixture(t)
	ctx := nativeQualificationRestoreContext(t.Context(), target)
	if err := consumeNativeQualificationRestoreLoad(t.Context(), target); err == nil {
		t.Fatal("retained frame minted load authority")
	}
	if err := consumeNativeQualificationRestoreLoad(ctx, target); err != nil {
		t.Fatal(err)
	}
	ctx = nativeQualificationRestoreContext(context.WithoutCancel(ctx), target)
	if err := consumeNativeQualificationRestoreLoad(ctx, target); err == nil {
		t.Fatal("rebuilding the original context replayed a consumed load")
	}
}

func nativeRestoreLoadRecordFixture(t *testing.T) (*nativeQualificationRestoreLoadJournal, nativeQualificationRestoreLoadRecord, nativeQualificationRestoreRecord, nativeQualificationCaptureRecord, nativeSnapshotBackingRecord, nativeLaunchRecord) {
	t.Helper()
	j, frame, ctx := nativeQualificationRestoreFixture(t)
	source, err := j.incoming.read(frame.CaptureInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := j.incoming.readCapture(source)
	if err != nil {
		t.Fatal(err)
	}
	capture.Info.MemBytes = int64(frame.RAMMB) << 20
	capture.Backing = BackingIdentity{Version: 1, Kernel: "sha256:" + strings.Repeat("a", 64), Base: "sha256:" + strings.Repeat("b", 64)}
	if err := j.incoming.writeCapture(source, capture); err != nil {
		t.Fatal(err)
	}
	initial := capture
	initial.CompletedAt, initial.Info, initial.Backing = time.Time{}, SnapshotInfo{}, BackingIdentity{}
	backings := nativeSnapshotBackingRecord{Version: 1, Capture: initial, Backing: capture.Backing}
	for i, name := range []string{"original-kernel", "original-base.ext4"} {
		backings.Images[i] = nativeSnapshotBackingImage{Epoch: uuid.NewString(), ReferenceID: uuid.NewString(), Identity: nativeLoopIdentity{Device: 1, Inode: uint64(10 + i)},
			Name: name, LogicalBytes: 4096, SHA256: strings.Repeat(string(rune('a'+i)), 64)}
	}
	if err := j.incoming.writeBackings(backings); err != nil {
		t.Fatal(err)
	}
	target, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.incoming.owner.prepare(nativeQualificationRestoreContext(ctx, target), qualificationLease(frame.InstanceID)); err != nil {
		t.Fatal(err)
	}
	target, err = j.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := j.incoming.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical.Authorized, physical.PID, physical.StartTime = true, 42, 101
	if err := j.incoming.owner.write(physical); err != nil {
		t.Fatal(err)
	}
	r := nativeQualificationRestoreLoadRecord{Version: 1, InstanceID: frame.InstanceID, IncomingGeneration: target.Generation, NativeGeneration: physical.Generation,
		KernelBootID: physical.KernelBootID, PID: physical.PID, StartTime: physical.StartTime, TargetSHA256: nativeRestoreTargetHash(target),
		CaptureSHA256: nativeRestoreEvidenceHash(capture), BackingsSHA256: nativeRestoreEvidenceHash(backings)}
	r.Cgroup = nativeHostHelperGroup{Path: nativeSnapshotMemoryPath(physical), Device: 3, Inode: 30}
	r.Phases[0] = target.AcceptedAt.Add(time.Millisecond)
	for i, name := range []string{backings.Images[0].Name, backings.Images[1].Name, memSnapshotName, vmstateSnapshotName, layerImageName} {
		r.Images[i] = nativeSnapshotBackingImage{Epoch: uuid.NewString(), ReferenceID: uuid.NewString(), Identity: nativeLoopIdentity{Device: 2, Inode: uint64(20 + i)}, Name: name, LogicalBytes: 4096, SHA256: strings.Repeat("c", 64)}
		if i < 2 {
			r.Images[i].LogicalBytes, r.Images[i].SHA256 = backings.Images[i].LogicalBytes, backings.Images[i].SHA256
		}
	}
	r.Images[2].LogicalBytes, r.Images[3].LogicalBytes = capture.Info.MemBytes, capture.Info.VMStateBytes
	if err := r.requireOriginal(target, capture, backings, physical); err != nil {
		t.Fatal(err)
	}
	return j.loads(), r, target, capture, backings, physical
}

func TestNativeQualificationRestoreLoadRecordRetainsOriginalEvidence(t *testing.T) {
	for _, change := range []string{"original", "incoming", "physical", "pid", "start", "capture", "backing", "borrowed_inode", "borrowed_epoch", "target_hash", "image_alias", "name", "ram", "lease_ram", "phase_skip", "phase_reverse", "expired"} {
		t.Run(change, func(t *testing.T) {
			_, r, target, capture, backings, physical := nativeRestoreLoadRecordFixture(t)
			switch change {
			case "incoming":
				target.Generation = uuid.NewString()
			case "physical":
				physical.Generation = uuid.NewString()
			case "pid":
				physical.PID++
			case "start":
				physical.StartTime++
			case "capture":
				capture.Info.StoredBytes++
			case "backing":
				backings.Images[0].SHA256 = strings.Repeat("f", 64)
			case "borrowed_inode":
				r.Images[0].Identity = backings.Images[0].Identity
			case "borrowed_epoch":
				r.Images[0].Epoch = backings.Images[0].Epoch
			case "target_hash":
				r.TargetSHA256 = strings.Repeat("0", 64)
			case "image_alias":
				r.Images[1].ReferenceID = r.Images[0].ReferenceID
			case "name":
				r.Images[2].Name = "candidate-memory"
			case "ram":
				r.Images[2].LogicalBytes--
			case "lease_ram":
				physical.Lease.MemoryMaxMiB++
				target.NativeLease = physical.Lease
				r.TargetSHA256 = nativeRestoreTargetHash(target)
			case "phase_skip":
				r.Phases[2] = r.Phases[0]
			case "phase_reverse":
				r.Phases[1] = r.Phases[0].Add(-time.Nanosecond)
			case "expired":
				r.Phases[0] = target.Deadline.Add(time.Nanosecond)
			}
			if err := r.requireOriginal(target, capture, backings, physical); (err == nil) != (change == "original") {
				t.Fatal(change, err)
			}
			target.Revoked = true
			if change == "original" && r.requireOriginal(target, capture, backings, physical) != nil {
				t.Fatal("revocation lost original evidence")
			}
		})
	}
}

func TestNativeQualificationRestoreLoadJournalRefusesReplayAndLostAcknowledgements(t *testing.T) {
	j, r, _, _, _, _ := nativeRestoreLoadRecordFixture(t)
	if err := j.begin(r); err != nil {
		t.Fatal(err)
	}
	if err := j.begin(r); err == nil {
		t.Fatal("load intent replayed")
	}
	if _, err := j.advance(r, nativeRestoreResumeStarted, r.Phases[0]); err == nil {
		t.Fatal("skipped load acknowledgement")
	}
	lost := errors.New("durable write acknowledgement lost")
	j.writeValue = func(path string, next nativeQualificationRestoreLoadRecord) error {
		return errors.Join(writeNativeJournalValue(path, next), lost)
	}
	if _, err := j.advance(r, nativeRestoreLoaded, r.Phases[0].Add(time.Millisecond)); !errors.Is(err, lost) {
		t.Fatal(err)
	}
	j.writeValue = nil
	if _, err := j.advance(r, nativeRestoreLoaded, r.Phases[0].Add(time.Millisecond)); err == nil {
		t.Fatal("uncertain acknowledgement replayed")
	}
	stored, err := j.read(r.InstanceID)
	if err != nil || stored.Phases[1].IsZero() || !stored.Phases[2].IsZero() {
		t.Fatal("lost acknowledgement was not retained", err)
	}
	if err := j.begin(stored); err == nil {
		t.Fatal("recovered phase minted producer authority")
	}
}

func TestNativeQualificationRestoreLoadJournalRejectsDamagedPrivateEvidence(t *testing.T) {
	for _, change := range []string{"original", "missing", "duplicate", "unknown", "short_images", "extra_phases", "trailing", "oversized", "symlink", "public"} {
		t.Run(change, func(t *testing.T) {
			j, r, _, _, _, _ := nativeRestoreLoadRecordFixture(t)
			if err := j.begin(r); err != nil {
				t.Fatal(err)
			}
			path, _ := j.path(r.InstanceID)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing":
				body = []byte(strings.Replace(string(body), `"version":1,`, "", 1))
			case "duplicate":
				body = append([]byte(`{"version":1,`), body[1:]...)
			case "unknown":
				body = append([]byte(`{"ready":true,`), body[1:]...)
			case "short_images", "extra_phases":
				var value map[string]any
				if err := json.Unmarshal(body, &value); err != nil {
					t.Fatal(err)
				}
				if change == "short_images" {
					value["images"] = value["images"].([]any)[:4]
				} else {
					value["phases"] = append(value["phases"].([]any), nil)
				}
				body, err = json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
			case "trailing":
				body = append(body, []byte(`{}`)...)
			case "oversized":
				body = append(body, []byte(strings.Repeat(" ", api.NativeQualificationRestoreLoadRecordMaxBytes))...)
			case "symlink":
				if err := os.Rename(path, path+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-retained", path); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if change != "symlink" && change != "public" {
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.read(r.InstanceID); (err == nil) != (change == "original") {
				t.Fatal(change, err)
			}
		})
	}
}
