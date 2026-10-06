//go:build linux

// adr: 568 — modeled authority tests and real descriptor copies grant no launch.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

type nativeRestoreStagingFixtureBackend struct{ *nativeWritableImageFixture }

func (b *nativeRestoreStagingFixtureBackend) PrepareRestoreInput(ctx context.Context, owner nativeLaunchRecord, root string, _ *os.File, name string, _ storage.ExclusiveArtifactReceipt) (nativeImagePreparation, error) {
	return b.PrepareWritable(ctx, owner, root, "", name)
}

func nativeRestoreStagingFixture(t *testing.T) (*JailerVMM, *nativeImageSourceJournal, nativeLaunchRecord, *nativeRestoreStagingFixtureBackend, string) {
	t.Helper()
	j, owner, original, root := nativeWritableJournalFixture(t)
	b := &nativeRestoreStagingFixtureBackend{nativeWritableImageFixture: original}
	j.backend, j.owner.imageSources = b, b
	r := &nativeProcessRecoveryRuntime{journal: j.owner, imageSources: b, owned: make(map[string]string)}
	if err := r.acquireDaemonOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.remember(owner)
	t.Cleanup(func() { _ = r.daemonLock.Close() })
	v := &JailerVMM{chrootBase: filepath.Dir(filepath.Dir(filepath.Dir(root))), fcName: "firecracker", nativeRecovery: r}
	return v, j, owner, b, root
}

func TestNativeRestoreStagingRequiresOriginalTargetAndCompleteInputs(t *testing.T) {
	for _, change := range []string{"original", "legacy", "backend", "recovered", "daemon", "generation", "boot", "lease", "root", "builder", "authorized", "revoked", "same_capture", "receipt", "completion", "backing", "backing_digest", "missing_input", "closed_input", "writable_input", "named_input", "size", "mode", "no_cloexec", "canceled"} {
		t.Run(change, func(t *testing.T) {
			f := nativeRestoreInputsFixture(t, nil)
			v, j, owner, b, root := nativeRestoreStagingFixture(t)
			err := withNativeSnapshotRestoreInputs(t.Context(), f.journal, f.backend, f.completed, f.root, func(inputs nativeSnapshotRestoreInputs) error {
				ctx := t.Context()
				completed := f.completed
				switch change {
				case "legacy":
					v.nativeRecovery = nil
				case "backend":
					v.nativeRecovery.imageSources = b.nativeImageBackendFixture
				case "recovered":
					delete(v.nativeRecovery.owned, owner.Lease.Instance)
				case "daemon":
					if err := v.nativeRecovery.daemonLock.Close(); err != nil {
						t.Fatal(err)
					}
				case "generation":
					owner.Generation = uuid.NewString()
				case "boot":
					owner.KernelBootID = uuid.NewString()
				case "lease":
					owner.Lease.MemoryMaxMiB++
				case "root":
					root += "-replacement"
				case "builder":
					owner.Lease.IsBuilder = true
				case "authorized":
					current := owner
					current.Authorized, current.PID, current.StartTime = true, 42, 101
					if err := j.owner.write(current); err != nil {
						t.Fatal(err)
					}
				case "revoked":
					if _, err := j.owner.revoke(ctx, owner.Lease.Instance); err != nil {
						t.Fatal(err)
					}
				case "same_capture":
					owner.Lease.Instance = completed.InstanceID
				case "receipt":
					if err := os.Remove(filepath.Join(f.journal.root, nativePublicationReceiptName(completed.CaptureID, "drive"))); err != nil {
						t.Fatal(err)
					}
				case "completion":
					completed.Info.StoredBytes++
				case "backing":
					inputs.Backing.Base += "-changed"
				case "backing_digest":
					body, err := json.Marshal(map[string]any{"version": inputs.Backing.Version, "kernel": inputs.Backing.Kernel, "base": inputs.Backing.Base})
					if err != nil || len(body) != len(f.bodies[3]) || bytes.Equal(body, f.bodies[3]) {
						t.Fatal("fixture must retain backing identity and length while changing original bytes", err)
					}
					mutateNativeRestoreFixtureInput(t, inputs.Files[3], func(file *os.File) error { _, err := file.WriteAt(body, 0); return err })
				case "missing_input":
					inputs.Files[2] = nil
				case "closed_input":
					if err := inputs.Files[2].Close(); err != nil {
						t.Fatal(err)
					}
				case "writable_input":
					if err := inputs.Files[2].Chmod(0o600); err != nil {
						t.Fatal(err)
					}
					fd, err := unix.Open(nativeImageFDPath(inputs.Files[2]), unix.O_RDWR|unix.O_CLOEXEC, 0)
					if err != nil {
						t.Fatal(err)
					}
					file := os.NewFile(uintptr(fd), "modeled-writable-replacement")
					defer file.Close()
					if err := file.Chmod(0o400); err != nil {
						t.Fatal(err)
					}
					inputs.Files[2] = file
				case "named_input":
					path := filepath.Join(f.root, "foreign.img")
					if err := os.WriteFile(path, f.bodies[2], 0o400); err != nil {
						t.Fatal(err)
					}
					file, err := os.OpenFile(path, os.O_RDONLY|unix.O_CLOEXEC, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					inputs.Files[2] = file
				case "size":
					mutateNativeRestoreFixtureInput(t, inputs.Files[2], func(file *os.File) error { return file.Truncate(1) })
				case "mode":
					if err := inputs.Files[2].Chmod(0o444); err != nil {
						t.Fatal(err)
					}
				case "no_cloexec":
					if _, err := unix.FcntlInt(inputs.Files[2].Fd(), unix.F_SETFD, 0); err != nil {
						t.Fatal(err)
					}
				case "canceled":
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = canceled
				}
				return v.stageNativeSnapshotRestoreInputs(ctx, owner, root, inputs, completed, f.journal)
			})
			if change == "original" {
				if err != nil || b.prepares != 3 || b.closes != 3 {
					t.Fatal("original target did not join three private preparations", err, b.prepares, b.closes)
				}
				records, err := j.records()
				if err != nil || len(records) != 3 {
					t.Fatal("staging lost private image epochs", len(records), err)
				}
				for _, record := range records {
					ref := record.References[0]
					if !record.Ready || !ref.Ready || ref.Link || ref.ReadOnly != (ref.Name != layerImageName) || ref.Owner != owner {
						t.Fatal("clone lost original target access intent")
					}
				}
				retired := retireNativeImageFixtureOwner(t, j, owner)
				if err := j.retireAll(t.Context(), retired); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || b.prepares != 0 {
				t.Fatal("invalid target/input set began staging", err, b.prepares)
			}
			if len(v.materialisedTmp) != 0 || len(v.bindMounts) != 0 {
				t.Fatal("receipt staging borrowed legacy in-memory ownership")
			}
		})
	}
}

func mutateNativeRestoreFixtureInput(t *testing.T, input *os.File, change func(*os.File) error) {
	t.Helper()
	if err := input.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(nativeImageFDPath(input), unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "modeled-input-mutation")
	if err := errors.Join(change(file), file.Chmod(0o400), file.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRestoreInputCloneVerifiesBytesWithoutChangingSource(t *testing.T) {
	for _, name := range []string{memSnapshotName, vmstateSnapshotName, layerImageName} {
		t.Run(name, func(t *testing.T) {
			f := nativeRestoreInputsFixture(t, nil)
			_, _, owner, _, root := nativeRestoreStagingFixture(t)
			base, disk := filepath.Dir(filepath.Dir(filepath.Dir(root))), t.TempDir()
			if err := errors.Join(os.MkdirAll(root, 0o700), os.Chmod(disk, 0o700)); err != nil {
				t.Fatal(err)
			}
			backend := linuxNativeImageSources{base: base, diskStagingRoot: disk}
			if err := withNativeSnapshotRestoreInputs(t.Context(), f.journal, f.backend, f.completed, f.root, func(inputs nativeSnapshotRestoreInputs) error {
				input, receipt := inputs.Files[0], inputs.Cohort.Objects[0].Object
				if _, err := input.Seek(7, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				p, err := backend.PrepareRestoreInput(t.Context(), owner, root, input, name, receipt)
				if err != nil {
					return err
				}
				clone := p.(*linuxNativeImagePreparation)
				body, err := io.ReadAll(io.NewSectionReader(clone.source, 0, receipt.LogicalBytes))
				if err != nil || !bytes.Equal(body, f.bodies[0]) {
					t.Fatal("descriptor clone changed original bytes", err)
				}
				original, _, err := nativeImageFileMetadata(input)
				offset, offsetErr := input.Seek(0, io.SeekCurrent)
				if err != nil || offsetErr != nil || offset != 7 || p.Identity() == original || p.PreferLink() {
					t.Fatal("clone changed source offset or borrowed its identity", err, offsetErr)
				}
				if _, err := clone.source.WriteAt([]byte("changed"), 0); err != nil {
					t.Fatal(err)
				}
				body = make([]byte, len(f.bodies[0]))
				if _, err := input.ReadAt(body, 0); err != nil || !bytes.Equal(body, f.bodies[0]) {
					t.Fatal("clone writes reached verified input", err)
				}
				return p.Close()
			}); err != nil {
				t.Fatal(err)
			}
			if entries, err := os.ReadDir(disk); err != nil || len(entries) != 0 {
				t.Fatal("unpublished clone acquired a persistent name", entries, err)
			}
		})
	}
}

func TestNativeRestoreInputCloneRefusesTamperedBytesBeforeEpoch(t *testing.T) {
	f := nativeRestoreInputsFixture(t, nil)
	_, _, owner, _, root := nativeRestoreStagingFixture(t)
	base, disk := filepath.Dir(filepath.Dir(filepath.Dir(root))), t.TempDir()
	if err := errors.Join(os.MkdirAll(root, 0o700), os.Chmod(disk, 0o700)); err != nil {
		t.Fatal(err)
	}
	b := linuxNativeImageSources{base: base, diskStagingRoot: disk}
	if err := withNativeSnapshotRestoreInputs(t.Context(), f.journal, f.backend, f.completed, f.root, func(inputs nativeSnapshotRestoreInputs) error {
		mutateNativeRestoreFixtureInput(t, inputs.Files[0], func(file *os.File) error { _, err := file.WriteAt([]byte("tamper"), 0); return err })
		p, err := b.PrepareRestoreInput(t.Context(), owner, root, inputs.Files[0], memSnapshotName, inputs.Cohort.Objects[0].Object)
		if !errors.Is(err, storage.ErrArtifactReceiptMismatch) || p != nil {
			t.Fatal("tampered anonymous input was promoted to staging", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(disk); err != nil || len(entries) != 0 {
		t.Fatal("failed verification left named data or cleanup authority", entries, err)
	}
}

func TestNativeRestoreStagingInterruptedProducerCannotReplay(t *testing.T) {
	for _, failure := range []string{"anchor_acknowledgement", "input_close"} {
		t.Run(failure, func(t *testing.T) {
			f := nativeRestoreInputsFixture(t, nil)
			v, j, owner, b, root := nativeRestoreStagingFixture(t)
			injected := errors.New("original staging acknowledgement lost")
			if failure == "input_close" {
				b.closeInput = func() error { return injected }
			} else {
				writes := 0
				j.writeValue = func(path string, record nativeImageSourceRecord) error {
					writes++
					if writes == 3 {
						return injected
					}
					return writeNativeJournalValue(path, record)
				}
			}
			// Inject the lost journal checkpoint on the shared staging primitive;
			// exercise producer Close through the complete VMM wrapper.
			err := withNativeSnapshotRestoreInputs(t.Context(), f.journal, f.backend, f.completed, f.root, func(inputs nativeSnapshotRestoreInputs) error {
				if failure == "anchor_acknowledgement" {
					_, err := j.stageRestoreInput(t.Context(), owner, root, inputs.Files[0], memSnapshotName, inputs.Cohort.Objects[0].Object)
					return err
				}
				return v.stageNativeSnapshotRestoreInputs(t.Context(), owner, root, inputs, f.completed, f.journal)
			})
			if !errors.Is(err, injected) {
				t.Fatal("uncertain original staging effect was promoted", err)
			}
			if b.prepares != 1 || b.closes != 1 {
				t.Fatal("uncertain first effect advanced to another clone", b.prepares, b.closes)
			}
			j.writeValue, b.closeInput = nil, nil
			err = withNativeSnapshotRestoreInputs(t.Context(), f.journal, f.backend, f.completed, f.root, func(inputs nativeSnapshotRestoreInputs) error {
				return v.stageNativeSnapshotRestoreInputs(t.Context(), owner, root, inputs, f.completed, f.journal)
			})
			if err == nil || b.prepares != 1 {
				t.Fatal("retry replayed an uncertain original image effect", err, b.prepares)
			}
			retired := retireNativeImageFixtureOwner(t, j, owner)
			if err := j.retireAll(t.Context(), retired); err != nil {
				t.Fatal(err)
			}
			if err := j.require(t.Context(), retired, true); err != nil {
				t.Fatal("original retirement did not join incomplete staging", err)
			}
		})
	}
}

func TestNativeDiskStagingReadOnlyClaimIsRestrictedToExclusiveSnapshotInputs(t *testing.T) {
	for _, name := range []string{memSnapshotName, vmstateSnapshotName, "foreign", layerImageName} {
		t.Run(name, func(t *testing.T) {
			p, b, record, point := nativeDiskStagingFixture(t)
			record.References[0].Name, record.References[0].ReadOnly, record.References[0].AddPerms = name, true, 0o044
			var err error
			record.Desired, err = desiredNativeImageMetadata(record)
			if err != nil {
				t.Fatal(err)
			}
			err = p.OwnAnonymousSource(record, point)
			if name == memSnapshotName || name == vmstateSnapshotName {
				if err != nil {
					t.Fatal(err)
				}
				if err := p.linkAnonymousSource(point); err != nil {
					t.Fatal(err)
				}
				if err := b.inventoryDiskStaging([]nativeImageSourceRecord{record}); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("arbitrary read-only anonymous disk claim was accepted")
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			if entries, err := os.ReadDir(b.diskStagingRoot); err != nil || len(entries) != 0 {
				t.Fatal("read-only disk claim retained a name after producer join", entries, err)
			}
		})
	}
}

func TestNativeDiskStagingReadOnlyDifferentBootCleanup(t *testing.T) {
	for _, name := range []string{memSnapshotName, vmstateSnapshotName} {
		t.Run(name, func(t *testing.T) {
			p, b, record, point := nativeDiskStagingFixture(t)
			// Model a previous kernel boot; this is not a real reboot test.
			record.KernelBoot = uuid.NewString()
			record.References[0].Owner.KernelBootID = record.KernelBoot
			record.References[0].Name, record.References[0].ReadOnly, record.References[0].AddPerms = name, true, 0o044
			p.owner = record.References[0].Owner
			var err error
			record.Desired, err = desiredNativeImageMetadata(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.OwnAnonymousSource(record, point); err != nil {
				t.Fatal(err)
			}
			if err := p.linkAnonymousSource(point); err != nil {
				t.Fatal(err)
			}
			if err := p.source.Chmod(os.FileMode(record.Desired.Mode)); err != nil {
				t.Fatal(err)
			}
			if err := b.inventoryDiskStaging(nil); err != nil {
				t.Fatal("different-boot exclusion failed to retire original read-only clone", err)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			if entries, err := os.ReadDir(b.diskStagingRoot); err != nil || len(entries) != 0 {
				t.Fatal("different-boot read-only claim retained persistent names", entries, err)
			}
		})
	}
}
