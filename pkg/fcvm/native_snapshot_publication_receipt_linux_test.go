//go:build linux

// adr: 568 — durable receipt inventory does not reconstruct a live producer.
package fcvm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func TestNativePublicationObjectReceiptsSurviveRestartWithoutAdoption(t *testing.T) {
	f, j, intent := nativePublicationDiskFixture(t)
	intent, err := j.Begin(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	object := nativeModeledArtifactReceipt(intent.Keys.Memory, []byte("original"))
	r, err := j.RecordObject(t.Context(), intent, "mem", object)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.RequireObject(t.Context(), intent, r); err != nil {
		t.Fatal(err)
	}
	if _, err := j.RecordObject(t.Context(), intent, "mem", object); !errors.Is(err, storage.ErrArtifactExists) {
		t.Fatal("receipt was replaced/adopted", err)
	}
	if err := os.RemoveAll(f.q.owner.root); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := newNativeSnapshotPublicationJournal(j.root, j.base, "").(*linuxNativeSnapshotPublicationJournal)
	if err := restarted.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.owner.Close() }()
	if err := restarted.RequireObject(t.Context(), intent, r); err != nil {
		t.Fatal("inventory lost original receipt", err)
	}
	f.v.nativeRecovery.publications = restarted
	if err := f.v.publishNativeSnapshotOutput(f.ctx, f.owner.Lease, "mem"); err == nil || f.b.opens != 0 {
		t.Fatal("receipt inventory granted live publication", err)
	}
}

func TestNativePublicationObjectReceiptRefusesCorruptionAndSubstitution(t *testing.T) {
	for _, change := range []string{"unknown", "duplicate", "null", "wrong_intent", "wrong_key", "wrong_generation", "receipt_duplicate", "local_missing_field", "alias", "replacement", "symlink", "missing_intent"} {
		t.Run(change, func(t *testing.T) {
			_, j, intent := nativePublicationDiskFixture(t)
			intent, err := j.Begin(t.Context(), intent)
			if err != nil {
				t.Fatal(err)
			}
			r, err := j.RecordObject(t.Context(), intent, "mem", nativeModeledArtifactReceipt(intent.Keys.Memory, []byte("original")))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(j.root, nativePublicationReceiptName(r.CaptureID, r.Kind))
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "unknown":
				body = append([]byte(`{"foreign":false,`), body[1:]...)
			case "duplicate":
				body = append([]byte(`{"version":1,`), body[1:]...)
			case "null":
				body = stringsBytesReplace(body, `"object":{`, `"object":null,"discarded":{`)
			case "wrong_intent":
				changed := r
				changed.IntentFile.Inode++
				body, err = json.Marshal(changed)
			case "wrong_key":
				changed := r
				changed.Object.Key += "-foreign"
				body, err = json.Marshal(changed)
			case "wrong_generation":
				body = stringsBytesReplace(body, `"generation":1`, `"generation":0`)
			case "receipt_duplicate":
				body = stringsBytesReplace(body, `"object":{`, `"object":{"version":1,`)
			case "local_missing_field":
				body = stringsBytesReplace(body, `"local":null`, `"local":{"device":1}`)
			case "alias":
				err = os.Link(path, filepath.Join(t.TempDir(), "alias"))
			case "replacement", "symlink":
				retained := filepath.Join(t.TempDir(), "retained")
				err = os.Rename(path, retained)
				if err == nil {
					if change == "replacement" {
						err = os.WriteFile(path, body, 0o600)
					} else {
						err = os.Symlink(retained, path)
					}
				}
			case "missing_intent":
				err = os.Remove(filepath.Join(j.root, intent.Capture.CaptureID+".json"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if change != "alias" && change != "replacement" && change != "symlink" && change != "missing_intent" {
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := j.RequireObject(t.Context(), intent, r); err == nil {
				t.Fatal("corrupt/substituted receipt retained evidence")
			}
			if err := j.owner.Close(); err != nil {
				t.Fatal(err)
			}
			restarted := newNativeSnapshotPublicationJournal(j.root, j.base, "").(*linuxNativeSnapshotPublicationJournal)
			if err := restarted.Acquire(t.Context()); err == nil {
				_ = restarted.owner.Close()
				t.Fatal("corrupt receipt inventory acquired ownership")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("inventory deleted retained receipt", err)
			}
		})
	}
}

func stringsBytesReplace(body []byte, old, replacement string) []byte {
	return []byte(strings.Replace(string(body), old, replacement, 1))
}

func TestNativePublicationObjectReceiptMustMatchOriginalIntentKey(t *testing.T) {
	_, j, intent := nativePublicationDiskFixture(t)
	intent, err := j.Begin(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	object := nativeModeledArtifactReceipt(intent.Keys.Drive, []byte("original"))
	if _, err := j.RecordObject(t.Context(), intent, "mem", object); err == nil {
		t.Fatal("foreign object key acquired original receipt")
	}
	entries, err := os.ReadDir(j.root)
	if err != nil || len(entries) != 1 {
		t.Fatal("invalid receipt left named output", entries, err)
	}
}
