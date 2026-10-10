// adr: 568 — captured drive names belong to the original native image epochs.
package fcvm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Order is kernel, then base. These are content and path witnesses, never
// permission to reuse the captured producer's physical or image ownership.
type nativeSnapshotBackingImage struct {
	Epoch        string             `json:"epoch"`
	ReferenceID  string             `json:"reference_id"`
	Identity     nativeLoopIdentity `json:"identity"`
	Name         string             `json:"name"`
	LogicalBytes int64              `json:"logical_bytes"`
	SHA256       string             `json:"sha256"`
}

func (image *nativeSnapshotBackingImage) UnmarshalJSON(body []byte) error {
	fields, err := nativeJournalObjectFields(body, []string{"epoch", "reference_id", "identity", "name", "logical_bytes", "sha256"})
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["identity"], []string{"device", "inode"}); err != nil {
		return err
	}
	type plain nativeSnapshotBackingImage
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	return d.Decode((*plain)(image))
}

func (image nativeSnapshotBackingImage) validate() error {
	if !nativeSnapshotBackingName(image.Name) {
		return errors.New("native snapshot backing: original image name is invalid")
	}
	return image.validateContentIdentity()
}

func (image nativeSnapshotBackingImage) validateContentIdentity() error {
	digest, err := hex.DecodeString(image.SHA256)
	if !canonicalNativeHelperID(image.Epoch) || !canonicalNativeHelperID(image.ReferenceID) || image.Identity.Device == 0 || image.Identity.Inode == 0 ||
		image.LogicalBytes <= 0 || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != image.SHA256 {
		return errors.New("native snapshot backing: original image identity is incomplete")
	}
	return nil
}

type nativeSnapshotBackingRecord struct {
	Version int                              `json:"version"`
	Capture nativeQualificationCaptureRecord `json:"capture"`
	Backing BackingIdentity                  `json:"backing"`
	Images  [2]nativeSnapshotBackingImage    `json:"images"`
}

func (r *nativeSnapshotBackingRecord) UnmarshalJSON(body []byte) error {
	fields, err := nativeJournalObjectFields(body, []string{"version", "capture", "backing", "images"})
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["backing"], []string{"version", "kernel", "base", "timer"}); err != nil {
		return err
	}
	var images []json.RawMessage
	if err := json.Unmarshal(fields["images"], &images); err != nil || len(images) != len(r.Images) {
		return errors.Join(err, errors.New("native snapshot backing: exactly two original images are required"))
	}
	for _, image := range images {
		fields, err := nativeJournalObjectFields(image, []string{"epoch", "reference_id", "identity", "name", "logical_bytes", "sha256"})
		if err != nil {
			return err
		}
		if _, err := nativeJournalObjectFields(fields["identity"], []string{"device", "inode"}); err != nil {
			return err
		}
	}
	type plain nativeSnapshotBackingRecord
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	return d.Decode((*plain)(r))
}

func nativeSnapshotBackingName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= api.NativeSnapshotBackingNameMaxBytes && filepath.Base(name) == name &&
		!strings.ContainsAny(name, "\\\x00") && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "capture-") &&
		name != layerImageName && name != memSnapshotName && name != vmstateSnapshotName
}

func (r nativeSnapshotBackingRecord) validate(capture nativeQualificationCaptureRecord) error {
	initial := capture
	initial.CompletedAt, initial.Info, initial.Backing = r.Capture.CompletedAt, SnapshotInfo{}, BackingIdentity{}
	if r.Version != 1 || !r.Capture.CompletedAt.IsZero() || r.Capture != initial || !r.Backing.complete() ||
		!capture.CompletedAt.IsZero() && r.Backing != capture.Backing {
		return errors.New("native snapshot backing: evidence differs from the original capture")
	}
	for i, image := range r.Images {
		want := r.Backing.Kernel
		if i == 1 {
			want = r.Backing.Base
		}
		if err := image.validate(); err != nil || want != "sha256:"+image.SHA256 {
			return errors.Join(err, errors.New("native snapshot backing: original image digest differs from capture"))
		}
	}
	a, b := r.Images[0], r.Images[1]
	if a.Name == b.Name || a.Epoch == b.Epoch || a.ReferenceID == b.ReferenceID || a.Identity == b.Identity || a.SHA256 == b.SHA256 {
		return errors.New("native snapshot backing: original kernel and base are aliased")
	}
	return nil
}

func (j *nativeQualificationJournal) backingPath(instance string) (string, error) {
	key, err := j.key(instance)
	return filepath.Join(j.root, "captures", "backings", key+".json"), err
}

func (j *nativeQualificationJournal) readBackings(capture nativeQualificationCaptureRecord) (r nativeSnapshotBackingRecord, result error) {
	path, err := j.backingPath(capture.InstanceID)
	if err != nil {
		return r, err
	}
	if err := errors.Join(checkNativeJournalPath(filepath.Dir(path), true), checkNativeJournalPath(path, false)); err != nil {
		return r, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return r, err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	body, err := io.ReadAll(io.LimitReader(file, api.NativeSnapshotBackingRecordMaxBytes+1))
	if err != nil || len(body) > api.NativeSnapshotBackingRecordMaxBytes {
		return r, errors.Join(err, errors.New("native snapshot backing: evidence exceeds its parser bound"))
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return r, err
	}
	return r, r.validate(capture)
}

// The caller holds the original incoming and physical locks. An uncertain
// first publication cannot be overwritten or adopted by a later producer.
func (j *nativeQualificationJournal) writeBackings(r nativeSnapshotBackingRecord) error {
	if err := r.validate(r.Capture); err != nil {
		return err
	}
	path, err := j.backingPath(r.Capture.InstanceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(filepath.Dir(path), true); err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("native snapshot backing: original capture already has backing evidence"))
	}
	return writeNativeJournalValue(path, r)
}

func (j *nativeQualificationJournal) validateBackings(ctx context.Context) error {
	root := filepath.Join(j.root, "captures", "backings")
	if err := checkNativeJournalPath(root, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".launch-") && entry.Type().IsRegular() {
			continue
		}
		instance, ok := strings.CutSuffix(entry.Name(), ".json")
		key, err := j.key(instance)
		if !ok || err != nil || key != instance || !entry.Type().IsRegular() {
			return errors.New("native snapshot backing: unexpected evidence entry")
		}
		lock, err := j.lock(ctx, instance)
		if err != nil {
			return err
		}
		incoming, err := j.read(instance)
		if err == nil {
			var capture nativeQualificationCaptureRecord
			capture, err = j.readCapture(incoming)
			if err == nil {
				_, err = j.readBackings(capture)
			}
		}
		if err := errors.Join(err, lock.Close()); err != nil {
			return err
		}
	}
	return ctx.Err()
}
