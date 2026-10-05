//go:build linux

// adr: 568 — persistent names have exact cleanup authority, never VM authority.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func nativeDiskStagingFixture(t *testing.T) (*linuxNativeImagePreparation, linuxNativeImageSources, nativeImageSourceRecord, string) {
	t.Helper()
	p, _ := nativeStagingPreparationFixture(t)
	base, disk := t.TempDir(), t.TempDir()
	if err := os.Chmod(disk, 0o700); err != nil {
		t.Fatal(err)
	}
	boot, err := nativeKernelBootID()
	if err != nil {
		t.Fatal(err)
	}
	owner := nativeLaunchRecord{Version: 1, Generation: uuid.NewString(), KernelBootID: boot, Lease: qualificationLease("disk-staging")}
	root := filepath.Join(base, "firecracker", owner.Lease.Instance, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootFile, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	p.root = rootFile
	t.Cleanup(func() { _ = rootFile.Close() })
	p.namespace, err = nativeLoopNamespaceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	p.owner, p.diskRoot = owner, disk
	metadata, err := p.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	record := nativeImageSourceRecord{Epoch: uuid.NewString(), KernelBoot: boot, Namespace: p.namespace, Identity: p.identity,
		Original: metadata, Applied: metadata, References: []nativeImageReference{{ID: uuid.NewString(), Owner: owner, Root: root, Name: layerImageName}}}
	record.Desired, err = desiredNativeImageMetadata(record)
	if err != nil {
		t.Fatal(err)
	}
	point := filepath.Join(base, ".native-processes", "image-sources", "points", record.Epoch)
	if err := os.MkdirAll(filepath.Dir(point), 0o700); err != nil {
		t.Fatal(err)
	}
	return p, linuxNativeImageSources{base: base, diskStagingRoot: disk}, record, point
}

func TestNativeDiskStagingRequiresDurableClaimBeforeLink(t *testing.T) {
	p, b, record, point := nativeDiskStagingFixture(t)
	if err := p.linkAnonymousSource(point); err == nil {
		t.Fatal("unowned disk name was created")
	}
	if err := p.OwnAnonymousSource(record, point); err != nil {
		t.Fatal(err)
	}
	claim, err := readNativeDiskImageClaim(b.diskStagingRoot, record.Epoch)
	if err != nil || !sameNativeDiskImageSource(claim, record) {
		t.Fatal("disk claim did not precede linking", err)
	}
	if err := p.linkAnonymousSource(point); err != nil {
		t.Fatal(err)
	}
	if err := b.inventoryDiskStaging([]nativeImageSourceRecord{record}); err != nil {
		t.Fatal(err)
	}
	if nativeWritableReadFile(t, p.source) != "original-private-output" {
		t.Fatal("disk staging changed anonymous bytes")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(b.diskStagingRoot); err != nil || len(entries) != 0 {
		t.Fatal("successful preparation retained a disk claim or name", entries, err)
	}
}

func TestNativeDiskStagingClaimDoesNotBorrowMutableBindingReferences(t *testing.T) {
	p, b, record, point := nativeDiskStagingFixture(t)
	if err := p.OwnAnonymousSource(record, point); err != nil {
		t.Fatal(err)
	}
	claim, err := readNativeDiskImageClaim(b.diskStagingRoot, record.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.linkAnonymousSource(point); err != nil {
		t.Fatal(err)
	}
	// These are the live journal mutations made after the initial durable
	// claim. They must not alter the producer's expected immutable claim.
	record.References[0].Target = nativeLoopIdentity{Device: 42, Inode: 43}
	record.References[0].MountID, record.References[0].Ready = 101, true
	if !reflect.DeepEqual(*p.diskClaim, claim) {
		t.Fatal("binding acknowledgement changed the producer's immutable disk claim")
	}
	if err := p.Close(); err != nil {
		t.Fatal("normal producer close could not retire original immutable claim", err)
	}
	if entries, err := os.ReadDir(b.diskStagingRoot); err != nil || len(entries) != 0 {
		t.Fatal("normal binding acknowledgement retained disk claim/source", entries, err)
	}
}

func TestNativeDiskStagingSameBootMissingAuthorityQuarantines(t *testing.T) {
	p, b, record, point := nativeDiskStagingFixture(t)
	if err := p.OwnAnonymousSource(record, point); err != nil {
		t.Fatal(err)
	}
	if err := p.linkAnonymousSource(point); err != nil {
		t.Fatal(err)
	}
	if err := b.inventoryDiskStaging(nil); err == nil || !strings.Contains(err.Error(), "quarantine") {
		t.Fatal("same-boot missing launch authority was silently removed", err)
	}
	if _, err := os.Lstat(p.staging); err != nil {
		t.Fatal("quarantine removed original named source", err)
	}
	changed := record
	changed.References = append([]nativeImageReference(nil), record.References...)
	changed.References[0].Owner.Generation = uuid.NewString()
	if err := b.inventoryDiskStaging([]nativeImageSourceRecord{changed}); err == nil {
		t.Fatal("later VM producer borrowed persistent disk ownership")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeDiskStagingRetiredOrDuplicatedAuthorityCannotBorrowClaim(t *testing.T) {
	p, b, record, point := nativeDiskStagingFixture(t)
	if err := p.OwnAnonymousSource(record, point); err != nil {
		t.Fatal(err)
	}
	if err := p.linkAnonymousSource(point); err != nil {
		t.Fatal(err)
	}
	retired := record
	retired.Removed = true
	if err := b.inventoryDiskStaging([]nativeImageSourceRecord{retired}); err == nil {
		t.Fatal("retired source retained persistent name ownership")
	}
	if err := b.inventoryDiskStaging([]nativeImageSourceRecord{record, record}); err == nil {
		t.Fatal("duplicated original records granted cleanup authority")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeDiskStagingRebootBoundaryAndFullInventory(t *testing.T) {
	for _, interrupted := range []string{"before_link", "after_link", "after_unlink"} {
		t.Run(interrupted, func(t *testing.T) {
			p, b, record, point := nativeDiskStagingFixture(t)
			// Model a previous boot's persistent claim. Real kernel reboot is
			// not performed by this test; the cleanup path uses current boot_id.
			record.KernelBoot = uuid.NewString()
			record.References[0].Owner.KernelBootID = record.KernelBoot
			p.owner = record.References[0].Owner
			if err := p.OwnAnonymousSource(record, point); err != nil {
				t.Fatal(err)
			}
			if interrupted != "before_link" {
				if err := p.linkAnonymousSource(point); err != nil {
					t.Fatal(err)
				}
			}
			if interrupted == "after_unlink" {
				if err := removeNativeImageStagingSource(p.staging, p.identity); err != nil {
					t.Fatal(err)
				}
			}
			unknown := filepath.Join(b.diskStagingRoot, "foreign-entry")
			if err := os.WriteFile(unknown, []byte("unowned"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := b.inventoryDiskStaging(nil); err == nil {
				t.Fatal("partial inventory authorized cleanup")
			}
			if _, err := os.Lstat(nativeDiskImageClaimPath(b.diskStagingRoot, record.Epoch)); err != nil {
				t.Fatal("invalid inventory changed an earlier claim", err)
			}
			if err := os.Remove(unknown); err != nil {
				t.Fatal(err)
			}
			if err := b.inventoryDiskStaging(nil); err != nil {
				t.Fatal(err)
			}
			if entries, err := os.ReadDir(b.diskStagingRoot); err != nil || len(entries) != 0 {
				t.Fatal("older-boot orphan survived exact cleanup", entries, err)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeDiskStagingCleanupPreservesChangedAuthority(t *testing.T) {
	for _, change := range []string{"inode", "alias", "symlink", "metadata", "claim", "directory"} {
		t.Run(change, func(t *testing.T) {
			p, b, record, point := nativeDiskStagingFixture(t)
			if err := p.OwnAnonymousSource(record, point); err != nil {
				t.Fatal(err)
			}
			if err := p.linkAnonymousSource(point); err != nil {
				t.Fatal(err)
			}
			path := p.staging
			switch change {
			case "inode", "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if change == "inode" {
					if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(point, path); err != nil {
					t.Fatal(err)
				}
			case "alias":
				if err := os.Link(path, filepath.Join(b.diskStagingRoot, "extra-alias")); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "claim":
				claim := *p.diskClaim
				claim.Version++
				if err := writeNativeJournalValue(nativeDiskImageClaimPath(b.diskStagingRoot, record.Epoch), claim); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Rename(b.diskStagingRoot, b.diskStagingRoot+"-original"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(b.diskStagingRoot + "-original") })
				if err := os.Mkdir(b.diskStagingRoot, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := writeNativeJournalValue(nativeDiskImageClaimPath(b.diskStagingRoot, record.Epoch), *p.diskClaim); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Close(); err == nil {
				t.Fatal("changed authority supplied cleanup permission")
			}
			if change != "directory" {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("uncertain cleanup deleted a source", err)
				}
			}
		})
	}
}

func TestNativeDiskStagingStrictClaimsAndExclusiveDaemonLock(t *testing.T) {
	p, b, record, point := nativeDiskStagingFixture(t)
	if err := p.OwnAnonymousSource(record, point); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(p.diskClaim)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		strings.Replace(string(body), `"version":1,`, "", 1),
		strings.Replace(string(body), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(body), `"inode":`, `"unknown":0,"inode":`, 1),
		string(body) + "{}",
	} {
		var decoded nativeDiskImageClaim
		if err := json.Unmarshal([]byte(data), &decoded); err == nil {
			t.Fatal("damaged ownership claim decoded")
		}
	}
	first, err := b.LockDiskStaging(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	second, err := b.LockDiskStaging(ctx)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("second daemon claimed the original disk journal", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeDiskStagingRuntimeRefusesLostDaemonOwnership(t *testing.T) {
	_, b, _, _ := nativeDiskStagingFixture(t)
	v := NewJailerVMM(b.base, time.Second).WithNativeImageStagingRoot(b.diskStagingRoot).WithNativeProcessRecovery()
	r := v.nativeRecovery
	if err := r.acquireDaemonOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer r.daemonLock.Close()
	if err := r.checkDaemonOwnership(); err != nil {
		t.Fatal(err)
	}
	if r.diskLock == nil {
		t.Fatal("configured production filesystem profile acquired no disk ownership")
	}
	if err := r.diskLock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.checkDaemonOwnership(); err == nil {
		t.Fatal("producer retained authority after losing its original disk lock")
	}
	if err := r.acquireDaemonOwnership(t.Context()); err == nil {
		t.Fatal("recovery reused lost ownership instead of refusing it")
	}
}
