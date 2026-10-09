//go:build linux || darwin

// adr: 568 — modeled ownership tests; no native snapshot load acceptance.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func nativeQualificationRestoreFixture(t *testing.T) (*nativeQualificationRestoreJournal, state.EnvironmentQualificationExecution, context.Context) {
	t.Helper()
	q, source, ctx := nativeQualificationFixture(t)
	incoming, err := q.claim(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.owner.prepare(nativeQualificationContext(ctx, incoming), qualificationLease(source.InstanceID)); err != nil {
		t.Fatal(err)
	}
	incoming, err = q.read(source.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	capture := nativeQualificationCaptureRecord{Version: 1, InstanceID: source.InstanceID, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, StartedAt: incoming.AcceptedAt.Add(time.Millisecond),
		CompletedAt: incoming.AcceptedAt.Add(2 * time.Millisecond), FCVersion: "1.7.0", Info: SnapshotInfo{MemBytes: 100, VMStateBytes: 50, StoredBytes: 200},
		Backing: BackingIdentity{Version: 1, Kernel: "sha256:modeled-kernel", Base: "sha256:modeled-base"}}
	writeCompleteNativeQualificationCapture(t, q, incoming, capture)
	if _, err := q.revoke(ctx, source); err != nil {
		t.Fatal(err)
	}
	physical, err := q.owner.read(source.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical.ExitConfirmed, physical.ResourcesRemoved = true, true
	if err := q.owner.write(physical); err != nil {
		t.Fatal(err)
	}
	target := source
	target.InstanceID, target.WakeID, target.CleanupToken, target.CaptureInstanceID = uuid.NewString(), uuid.NewString(), uuid.NewString(), source.InstanceID
	return q.restores(), target, ctx
}

func TestNativeQualificationRestoreClaimRequiresCapturedFirecrackerVersion(t *testing.T) {
	for _, running := range []string{"", " 1.7.0", "1.8.0"} {
		t.Run(fmt.Sprintf("running_%q", running), func(t *testing.T) {
			j, frame, ctx := nativeQualificationRestoreFixture(t)
			if _, err := j.claim(ctx, frame, running); err == nil {
				t.Fatal("restore target accepted an absent or incompatible Firecracker version")
			}
			path, err := j.path(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("incompatible version published restore authority", err)
			}
			if _, err := j.incoming.owner.read(frame.InstanceID); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("incompatible version created a native target", err)
			}
		})
	}
}

func TestNativeQualificationRestoreBindingRequiresExactGuestRAMBeforePublication(t *testing.T) {
	for _, delta := range []int{-1, 1, 8} {
		t.Run(fmt.Sprintf("delta_%d", delta), func(t *testing.T) {
			j, frame, ctx := nativeQualificationRestoreFixture(t)
			r, err := j.claim(ctx, frame, "1.7.0")
			if err != nil {
				t.Fatal(err)
			}
			lease := qualificationLease(frame.InstanceID)
			lease.MemoryMaxMiB = frame.RAMMB + delta
			if err := j.incoming.owner.prepare(nativeQualificationRestoreContext(ctx, r), lease); err == nil {
				t.Fatal("restore admitted a different guest RAM reservation")
			}
			retained, err := j.read(frame.InstanceID)
			if err != nil || retained.NativeGeneration != "" || retained.NativeLease != (Lease{}) {
				t.Fatal("RAM refusal published a physical binding", err)
			}
			if _, err := j.incoming.owner.read(frame.InstanceID); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("RAM refusal published a physical owner", err)
			}
		})
	}
}

func TestNativeQualificationRestoreDistinctBindingAndRetirement(t *testing.T) {
	j, frame, ctx := nativeQualificationRestoreFixture(t)
	r, err := j.claim(ctx, frame, "1.7.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.claim(ctx, frame, "1.7.0"); err == nil {
		t.Fatal("duplicate restore delivery acquired a producer")
	}
	lease := qualificationLease(frame.InstanceID)
	producer := nativeQualificationRestoreContext(ctx, r)
	j.incoming.owner.writeRecord = func(path string, physical nativeLaunchRecord) error {
		bound, err := j.read(frame.InstanceID)
		if err != nil || bound.NativeGeneration != physical.Generation || bound.Capture != r.Capture || !sameNativePhysicalLease(bound.NativeLease, lease) {
			t.Fatal("physical publication preceded durable restore binding", err)
		}
		return writeNativeLaunchRecord(path, physical)
	}
	if err := j.incoming.owner.prepare(producer, lease); err != nil {
		t.Fatal(err)
	}
	j.incoming.owner.writeRecord = nil
	bound, err := j.read(frame.InstanceID)
	if err != nil || bound.NativeGeneration == r.Generation || bound.NativeGeneration == r.Capture.NativeGeneration || bound.Capture != r.Capture {
		t.Fatal("target borrowed capture generation or evidence", err)
	}
	if err := j.incoming.owner.prepare(producer, lease); err == nil {
		t.Fatal("restore producer prepared a second physical generation")
	}
	ticket, err := j.incoming.owner.beginLaunch(producer, lease)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, stop := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err = j.revoke(cleanup, frame)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("restore revocation bypassed physical launch gate", err)
	}
	if err := ticket.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := j.incoming.owner.replace(producer, lease, bound.NativeGeneration, false); err == nil {
		t.Fatal("restore qualification acquired generic cold fallback")
	}
	if _, err := j.revoke(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	if _, err := j.retirement(t.Context(), frame); err == nil {
		t.Fatal("incoming revocation became target retirement proof")
	}
	physical, err := j.incoming.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical.ExitConfirmed, physical.ResourcesRemoved = true, true
	if err := j.incoming.owner.write(physical); err != nil {
		t.Fatal(err)
	}
	proof, err := j.retirement(t.Context(), frame)
	if err != nil || proof.ReceiptID != r.Generation || proof.NativeGeneration != bound.NativeGeneration || proof.NativeGeneration == r.Capture.NativeGeneration {
		t.Fatal("target retirement borrowed producer proof", err)
	}
	again, err := j.retirement(t.Context(), frame)
	if err != nil || again != proof {
		t.Fatal("retirement retry changed target proof", err)
	}
	if ticket, err := j.incoming.owner.beginLaunch(producer, lease); err == nil {
		_ = ticket.close()
		t.Fatal("delayed producer bypassed restore revocation")
	}
}

func TestNativeQualificationRestoreRequiresRetiredExactCapture(t *testing.T) {
	for _, change := range []string{"capture", "instance_alias", "wake", "cleanup", "node", "ram", "artifact", "attempt", "graph", "source", "capture_completion", "source_revocation", "physical_exit", "physical_resources", "physical_generation", "physical_lease", "deadline", "canceled"} {
		t.Run(change, func(t *testing.T) {
			j, frame, ctx := nativeQualificationRestoreFixture(t)
			source, err := j.incoming.read(frame.CaptureInstanceID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "capture":
				frame.CaptureInstanceID = uuid.NewString()
			case "instance_alias":
				frame.InstanceID = strings.ReplaceAll(frame.CaptureInstanceID, "-", "")
			case "wake":
				frame.WakeID = source.Execution.WakeID
			case "cleanup":
				frame.CleanupToken = source.Execution.CleanupToken
			case "node":
				frame.NodeID = uuid.NewString()
			case "ram":
				frame.RAMMB++
			case "artifact":
				frame.Artifact.RootfsBytes++
			case "attempt":
				frame.Attempt++
			case "graph":
				frame.GraphID = uuid.NewString()
			case "source":
				frame.SourceID = uuid.NewString()
			case "capture_completion":
				capture, err := j.incoming.readCapture(source)
				if err != nil {
					t.Fatal(err)
				}
				capture.CompletedAt, capture.Info, capture.Backing = time.Time{}, SnapshotInfo{}, BackingIdentity{}
				writeTamperedNativeQualificationCapture(t, j.incoming, source, capture)
			case "source_revocation":
				source.Revoked = false
				if err := j.incoming.write(source); err != nil {
					t.Fatal(err)
				}
			case "physical_exit", "physical_resources", "physical_generation", "physical_lease":
				physical, err := j.incoming.owner.read(frame.CaptureInstanceID)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "physical_exit":
					physical.ExitConfirmed, physical.ResourcesRemoved = false, false
				case "physical_resources":
					physical.ResourcesRemoved = false
				case "physical_generation":
					physical.Generation = uuid.NewString()
				case "physical_lease":
					physical.Lease.MemoryMaxMiB++
				}
				if err := j.incoming.owner.write(physical); err != nil {
					t.Fatal(err)
				}
			case "deadline":
				ctx = t.Context()
			case "canceled":
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelCtx
			}
			if _, err := j.claim(ctx, frame, "1.7.0"); err == nil {
				t.Fatal("changed authority admitted a restore target")
			}
			path, err := j.path(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected restore published incoming authority", err)
			}
		})
	}
}

func TestNativeQualificationRestoreProfileExcludesGenericAndCaptureProducers(t *testing.T) {
	j, frame, ctx := nativeQualificationRestoreFixture(t)
	r, err := j.claim(ctx, frame, "1.7.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range []string{frame.InstanceID, strings.ToUpper(frame.InstanceID), strings.ReplaceAll(frame.InstanceID, "-", "")} {
		if err := j.incoming.owner.prepare(ctx, qualificationLease(instance)); err == nil {
			t.Fatal("generic native preparation borrowed restore UUID", instance)
		}
	}
	sourceFrame := frame
	sourceFrame.CaptureInstanceID = ""
	if _, err := j.incoming.claim(ctx, sourceFrame); err == nil {
		t.Fatal("capture producer borrowed a restore target")
	}
	if _, err := j.incoming.revoke(ctx, sourceFrame); err == nil {
		t.Fatal("capture cleanup borrowed a restore target")
	}
	if err := j.incoming.owner.prepare(nativeQualificationContext(ctx, nativeQualificationRecord{Execution: sourceFrame}), qualificationLease(frame.InstanceID)); err == nil {
		t.Fatal("capture capability borrowed restore authority")
	}
	producer := nativeQualificationRestoreContext(ctx, r)
	producer = nativeQualificationContext(producer, nativeQualificationRecord{Execution: sourceFrame})
	if err := j.incoming.owner.prepare(producer, qualificationLease(frame.InstanceID)); err == nil {
		t.Fatal("ambiguous capture/restore context acquired a producer")
	}
}

func TestNativeQualificationRestorePublicationUncertaintyRetainsOriginalLease(t *testing.T) {
	j, frame, ctx := nativeQualificationRestoreFixture(t)
	r, err := j.claim(ctx, frame, "1.7.0")
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("physical publication acknowledgement lost")
	j.incoming.owner.writeRecord = func(string, nativeLaunchRecord) error { return cause }
	lease := qualificationLease(frame.InstanceID)
	producer := nativeQualificationRestoreContext(ctx, r)
	if err := j.incoming.owner.prepare(producer, lease); !errors.Is(err, cause) {
		t.Fatal("uncertain physical publication ignored", err)
	}
	j.incoming.owner.writeRecord = nil
	physical, err := j.incoming.owner.records(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	planned, err := j.incoming.recoveryLeases(t.Context(), physical)
	if err != nil || len(planned) != 1 || !sameNativePhysicalLease(planned[0], lease) {
		t.Fatal("uncertain target charge lost on restart", planned, err)
	}
	if _, err := j.revoke(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	if err := j.incoming.owner.prepare(producer, lease); err == nil {
		t.Fatal("delayed restore recreated an uncertain target")
	}
	if _, err := j.retirement(t.Context(), frame); err == nil {
		t.Fatal("missing physical record fabricated native retirement")
	}
}

func TestNativeQualificationRestoreLostClaimAcknowledgementAndEarlyRevoke(t *testing.T) {
	for _, change := range []string{"lost_ack", "early_revoke"} {
		t.Run(change, func(t *testing.T) {
			j, frame, ctx := nativeQualificationRestoreFixture(t)
			if change == "lost_ack" {
				j.writeValue = func(path string, r nativeQualificationRestoreRecord) error {
					return errors.Join(writeNativeJournalValue(path, r), errors.New("acknowledgement lost"))
				}
				if _, err := j.claim(ctx, frame, "1.7.0"); err == nil {
					t.Fatal("lost incoming acknowledgement ignored")
				}
			} else if _, err := j.revoke(t.Context(), frame); err != nil {
				t.Fatal(err)
			}
			j = j.incoming.restores()
			if _, err := j.claim(ctx, frame, "1.7.0"); err == nil {
				t.Fatal("restart replayed an uncertain or revoked incoming target")
			}
			if _, err := j.revoke(t.Context(), frame); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeQualificationRestoreStrictPrivateJournal(t *testing.T) {
	j, frame, ctx := nativeQualificationRestoreFixture(t)
	r, err := j.claim(ctx, frame, "1.7.0")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip nativeQualificationRestoreRecord
	if err := json.Unmarshal(data, &roundtrip); err != nil || roundtrip.Execution != r.Execution || roundtrip.Capture != r.Capture ||
		roundtrip.Generation != r.Generation || !roundtrip.AcceptedAt.Equal(r.AcceptedAt) || !roundtrip.Deadline.Equal(r.Deadline) {
		t.Fatal("restore profile lost original target identity", err)
	}
	for _, diagnostic := range []string{fmt.Sprintf("%v", r), fmt.Sprintf("%+v", r), fmt.Sprintf("%#v", r)} {
		if strings.Contains(diagnostic, frame.CleanupToken) {
			t.Fatal("restore diagnostic exposed cleanup authority")
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		saved := fields[name]
		delete(fields, name)
		malformed, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(malformed, &roundtrip); err == nil {
			t.Fatal("missing restore field accepted", name)
		}
		fields[name] = saved
	}
	path, err := j.path(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := j.read(frame.InstanceID); err == nil {
		t.Fatal("public restore capability accepted")
	}
	if err := j.incoming.owner.prepare(ctx, qualificationLease(frame.InstanceID)); err == nil {
		t.Fatal("damaged restore journal released generic ownership")
	}
}

func TestNativeQualificationRestoreRechecksCaptureBeforeBindingAndKeepsOwnCleanup(t *testing.T) {
	j, frame, ctx := nativeQualificationRestoreFixture(t)
	r, err := j.claim(ctx, frame, "1.7.0")
	if err != nil {
		t.Fatal(err)
	}
	source, err := j.incoming.read(frame.CaptureInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := j.incoming.readCapture(source)
	if err != nil {
		t.Fatal(err)
	}
	changed := capture
	changed.Info.StoredBytes++
	writeTamperedNativeQualificationCapture(t, j.incoming, source, changed)
	producer := nativeQualificationRestoreContext(ctx, r)
	lease := qualificationLease(frame.InstanceID)
	if err := j.incoming.owner.prepare(producer, lease); err == nil {
		t.Fatal("changed completion borrowed earlier restore admission")
	}
	writeTamperedNativeQualificationCapture(t, j.incoming, source, capture)
	if err := j.incoming.owner.prepare(producer, lease); err != nil {
		t.Fatal(err)
	}
	physical, err := j.incoming.owner.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	physical.Generation = r.Capture.NativeGeneration
	if _, err := j.recoveryLeases(t.Context(), []nativeLaunchRecord{physical}); err == nil {
		t.Fatal("target recovery adopted the source generation")
	}
	changedFrame := frame
	changedFrame.CleanupToken = uuid.NewString()
	if _, err := j.revoke(t.Context(), changedFrame); err == nil {
		t.Fatal("changed cleanup token revoked target authority")
	}
	path, err := j.incoming.capturePath(frame.CaptureInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := j.revoke(t.Context(), frame); err != nil {
		t.Fatal("target cleanup became dependent on damaged source evidence", err)
	}
}

func TestNativeQualificationRestoreCannotAdoptPriorPhysicalOrCaptureProfile(t *testing.T) {
	for _, profile := range []string{"ordinary", "capture"} {
		t.Run(profile, func(t *testing.T) {
			j, frame, ctx := nativeQualificationRestoreFixture(t)
			if profile == "ordinary" {
				if err := j.incoming.owner.prepare(ctx, qualificationLease(frame.InstanceID)); err != nil {
					t.Fatal(err)
				}
			} else {
				captureFrame := frame
				captureFrame.CaptureInstanceID = ""
				if _, err := j.incoming.claim(ctx, captureFrame); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.claim(ctx, frame, "1.7.0"); err == nil {
				t.Fatal("restore adopted prior instance ownership")
			}
		})
	}
}
