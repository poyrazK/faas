//go:build linux || darwin

package fcvm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativeBackingRecordFixture(t *testing.T) (nativeCaptureOutputFixture, nativeSnapshotBackingRecord) {
	t.Helper()
	f := nativeCaptureOutputsFixture(t)
	r := nativeSnapshotBackingRecord{Version: 1, Capture: f.capture,
		Backing: BackingIdentity{Version: 1, Kernel: "sha256:" + strings.Repeat("a", 64), Base: "sha256:" + strings.Repeat("b", 64)}}
	for i, name := range []string{"original-vmlinux", "original-base.ext4"} {
		r.Images[i] = nativeSnapshotBackingImage{Epoch: uuid.NewString(), ReferenceID: uuid.NewString(), Name: name,
			Identity: nativeLoopIdentity{Device: 11, Inode: uint64(20 + i)}, LogicalBytes: 17, SHA256: strings.Repeat(string(rune('a'+i)), 64)}
	}
	return f, r
}

func TestNativeSnapshotBackingRecordBindsExactOriginalCaptureAndNames(t *testing.T) {
	for _, change := range []string{"original", "version", "capture", "physical", "start", "complete", "digest", "digest_case", "size", "epoch", "reference", "identity", "alias_name", "alias_inode", "alias_epoch", "path", "reserved", "output", "oversized_name"} {
		t.Run(change, func(t *testing.T) {
			f, r := nativeBackingRecordFixture(t)
			switch change {
			case "version":
				r.Version++
			case "capture":
				r.Capture.CaptureID = uuid.NewString()
			case "physical":
				r.Capture.NativeGeneration = uuid.NewString()
			case "start":
				r.Capture.StartedAt = r.Capture.StartedAt.Add(time.Millisecond)
			case "complete":
				r.Capture.CompletedAt = time.Now()
			case "digest":
				r.Images[1].SHA256 = strings.Repeat("c", 64)
			case "digest_case":
				r.Images[1].SHA256 = strings.Repeat("B", 64)
			case "size":
				r.Images[1].LogicalBytes = 0
			case "epoch":
				r.Images[1].Epoch = "unknown"
			case "reference":
				r.Images[1].ReferenceID = uuid.Nil.String()
			case "identity":
				r.Images[1].Identity = nativeLoopIdentity{}
			case "alias_name":
				r.Images[1].Name = r.Images[0].Name
			case "alias_inode":
				r.Images[1].Identity = r.Images[0].Identity
			case "alias_epoch":
				r.Images[1].Epoch = r.Images[0].Epoch
			case "path":
				r.Images[1].Name = "../base"
			case "reserved":
				r.Images[1].Name = layerImageName
			case "output":
				r.Images[1].Name = "capture-" + uuid.NewString() + "-mem"
			case "oversized_name":
				r.Images[1].Name = strings.Repeat("x", api.NativeSnapshotBackingNameMaxBytes+1)
			}
			if err := r.validate(f.capture); (err == nil) != (change == "original") {
				t.Fatal("changed original backing evidence accepted", change, err)
			}
		})
	}
}

func TestNativeSnapshotBackingJournalRetainsFirstPublicationAndRejectsMalformedEvidence(t *testing.T) {
	for _, change := range []string{"original", "missing", "unknown", "duplicate", "short_images", "trailing", "oversized", "public", "symlink"} {
		t.Run(change, func(t *testing.T) {
			f, r := nativeBackingRecordFixture(t)
			if err := f.q.writeBackings(r); err != nil {
				t.Fatal(err)
			}
			path, _ := f.q.backingPath(f.capture.InstanceID)
			if err := f.q.writeBackings(r); err == nil {
				t.Fatal("uncertain original publication was replayed")
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing":
				body = []byte(strings.Replace(string(body), `"version":1,`, "", 1))
			case "unknown":
				body = append([]byte(`{"unknown":true,`), body[1:]...)
			case "duplicate":
				body = append([]byte(`{"version":1,`), body[1:]...)
			case "short_images":
				var data map[string]any
				if err := json.Unmarshal(body, &data); err != nil {
					t.Fatal(err)
				}
				data["images"] = data["images"].([]any)[:1]
				body, err = json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
			case "trailing":
				body = append(body, []byte(" {}")...)
			case "oversized":
				body = append(body, []byte(strings.Repeat(" ", api.NativeSnapshotBackingRecordMaxBytes))...)
			case "public":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other := filepath.Join(t.TempDir(), "other.json")
				if err := os.Rename(path, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			}
			if change != "public" && change != "symlink" {
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.q.readBackings(f.capture)
			if (err == nil) != (change == "original") || change == "original" && got != r {
				t.Fatal("malformed original evidence accepted", change, err)
			}
			if err := f.lock.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.q.validateCaptures(t.Context()); (err == nil) != (change == "original") {
				t.Fatal("inventory borrowed damaged backing evidence", err)
			}
			if change == "original" {
				completed := f.capture
				completed.Info, completed.Backing, completed.CompletedAt = SnapshotInfo{MemBytes: 1, VMStateBytes: 1, StoredBytes: 1}, r.Backing, time.Now()
				if got, err := f.q.readBackings(completed); err != nil || got != r {
					t.Fatal("completion lost original backing receipt", err)
				}
				completed.Backing.Base += "-changed"
				if _, err := f.q.readBackings(completed); err == nil {
					t.Fatal("backing witness adopted a changed capture")
				}
			}
		})
	}
}

func TestNativeSnapshotBackingMissingEvidenceDoesNotConferRestorePermission(t *testing.T) {
	f := nativeCaptureOutputsFixture(t)
	if _, err := f.q.readBackings(f.capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
