//go:build linux || darwin

// adr: 568 — portable output readers do not establish native mount acceptance.
package fcvm

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func nativeReadableCaptureFixture(t *testing.T) (nativeCaptureOutputFixture, nativeImageSourceRecord) {
	t.Helper()
	f := nativeCaptureOutputsFixture(t)
	if _, err := f.v.stageNativeSnapshotOutputs(f.ctx, f.owner.Lease, f.directory); err != nil {
		t.Fatal(err)
	}
	records, err := f.j.records()
	if err != nil {
		t.Fatal(err)
	}
	var memory nativeImageSourceRecord
	for _, record := range records {
		oldPath, oldIdentity := f.j.path(record), record.Identity
		if err := os.WriteFile(f.j.anchor(record), []byte(record.References[0].Name), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(f.j.anchor(record))
		if err != nil {
			t.Fatal(err)
		}
		identity, err := resourceFileID(info)
		if err != nil {
			t.Fatal(err)
		}
		record.Identity = nativeLoopIdentity{Device: identity.Device, Inode: identity.Inode}
		f.b.clones[record.Identity] = f.b.clones[oldIdentity]
		delete(f.b.clones, oldIdentity)
		if err := f.j.write(record); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(oldPath); err != nil {
			t.Fatal(err)
		}
		if record.References[0].Name == "capture-"+f.capture.CaptureID+"-mem" {
			memory = record
		}
	}
	return f, memory
}

func TestNativeSnapshotOutputInputReadsDistinctOriginalInodes(t *testing.T) {
	f, _ := nativeReadableCaptureFixture(t)
	for _, kind := range []string{"mem", "vmstate"} {
		if err := f.v.withNativeSnapshotOutput(f.ctx, f.owner.Lease, kind, func(file *os.File) error {
			body, err := io.ReadAll(file)
			if string(body) != "capture-"+f.capture.CaptureID+"-"+kind {
				t.Errorf("output reader substituted bytes: %q", body)
			}
			if _, writeErr := file.WriteAt([]byte("mutation"), 0); writeErr == nil {
				t.Error("publication descriptor permits writing")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.b.output.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("output descriptor escaped its consumer", err)
		}
	}
	if f.v.checkEnvironmentQualificationSnapshotSupport() == nil {
		t.Fatal("read primitive enabled incomplete capture")
	}
}

func TestNativeSnapshotOutputInputRejectsChangedAuthorityBeforeOpen(t *testing.T) {
	for _, name := range []string{"unknown_kind", "missing_capability", "foreign_capture", "foreign_native_generation", "foreign_kernel_boot", "completed_capture", "revoked", "restarted_process", "retired_output", "incomplete_binding", "duplicate_owner", "empty_output", "substituted_inode", "daemon_restart"} {
		t.Run(name, func(t *testing.T) {
			f, memory := nativeReadableCaptureFixture(t)
			ctx, kind := f.ctx, "mem"
			switch name {
			case "unknown_kind":
				kind = "../mem"
			case "missing_capability":
				ctx = t.Context()
			case "foreign_capture", "foreign_native_generation", "foreign_kernel_boot", "completed_capture":
				capture := f.capture
				switch name {
				case "foreign_capture":
					capture.CaptureID = uuid.NewString()
				case "foreign_native_generation":
					capture.NativeGeneration = uuid.NewString()
				case "foreign_kernel_boot":
					capture.KernelBootID = uuid.NewString()
				case "completed_capture":
					capture.CompletedAt = time.Now()
				}
				ctx = nativeSnapshotCaptureContext(ctx, f.incoming, capture, f.owner)
			case "revoked", "restarted_process":
				owner := f.owner
				owner.Revoked = name == "revoked"
				if name == "restarted_process" {
					owner.StartTime++
				}
				if err := f.q.owner.write(owner); err != nil {
					t.Fatal(err)
				}
			case "retired_output", "incomplete_binding":
				memory.References[0].TargetRemoved = name == "retired_output"
				memory.References[0].Ready = name != "incomplete_binding"
				if name == "retired_output" {
					memory.Desired = memory.Original
				}
				if err := f.j.write(memory); err != nil {
					t.Fatal(err)
				}
			case "duplicate_owner":
				memory.References = append(memory.References, memory.References[0])
				memory.References[1].ID = uuid.NewString()
				// A duplicated writable reference is corrupt journal data,
				// rejected by normal publication before it can be persisted.
				if err := writeNativeJournalValue(f.j.path(memory), memory); err != nil {
					t.Fatal(err)
				}
			case "empty_output", "substituted_inode":
				if name == "substituted_inode" {
					if err := os.Remove(f.j.anchor(memory)); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(f.j.anchor(memory), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "daemon_restart":
				delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
			}
			if err := f.v.withNativeSnapshotOutput(ctx, f.owner.Lease, kind, func(*os.File) error { t.Fatal("stale output reached publication"); return nil }); err == nil {
				t.Fatal("changed authority opened output")
			}
			wantOpens := 0
			if name == "empty_output" || name == "substituted_inode" {
				wantOpens = 1
				if _, err := f.b.output.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("rejected output descriptor leaked", err)
				}
			}
			if f.b.opens != wantOpens {
				t.Fatalf("output opens=%d want=%d", f.b.opens, wantOpens)
			}
		})
	}
}

func TestNativeSnapshotOutputInputClosesOnFailureAndCancellation(t *testing.T) {
	for _, name := range []string{"backend_error", "nil_output", "consumer_error", "consumer_cancel", "consumer_close"} {
		t.Run(name, func(t *testing.T) {
			f, _ := nativeReadableCaptureFixture(t)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			injected := errors.New("publication failed")
			f.b.openOutput = func(_ nativeImageSourceRecord, _ nativeImageReference, point string) (*os.File, error) {
				if name == "nil_output" {
					return nil, nil
				}
				file, err := os.OpenFile(point, os.O_RDONLY, 0)
				if name == "backend_error" {
					err = errors.Join(err, injected)
				}
				return file, err
			}
			err := f.v.withNativeSnapshotOutput(ctx, f.owner.Lease, "mem", func(file *os.File) error {
				if name == "consumer_cancel" {
					cancel()
					return nil
				}
				if name == "consumer_close" {
					return file.Close()
				}
				return injected
			})
			if err == nil {
				t.Fatal("failed publication returned success")
			}
			if f.b.output != nil {
				if _, err := f.b.output.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("failed publication leaked its descriptor", err)
				}
			}
		})
	}
}

func TestNativeSnapshotOutputInputPinsAuthorityUntilConsumerCloses(t *testing.T) {
	f, _ := nativeReadableCaptureFixture(t)
	if err := f.v.withNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem", func(*os.File) error {
		bounded, stop := context.WithTimeout(f.ctx, 30*time.Millisecond)
		defer stop()
		if lock, err := f.q.owner.lock(bounded, f.owner.Lease.Instance); err == nil {
			_ = lock.Close()
			t.Fatal("physical authority retired while output reader was active")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := f.q.owner.lock(f.ctx, f.owner.Lease.Instance)
	if err != nil {
		t.Fatal("output reader retained physical authority", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
