// adr: 568 — uncertain restore effects never grant replay or readiness.
package fcvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	nativeRestoreLoadStarted = iota
	nativeRestoreLoaded
	nativeRestoreResumeStarted
	nativeRestoreResumed
	nativeRestoreHookStarted
	nativeRestoreHookCompleted
	nativeRestorePhaseCount
)

// Images are kernel, base, memory, device state, private drive. Phases record
// intent before each effect and acknowledgement afterwards. Neither inventory
// nor a completed record is a producer, guest-readiness or activation permit.
type nativeQualificationRestoreLoadRecord struct {
	Version            int                                `json:"version"`
	InstanceID         string                             `json:"instance_id"`
	IncomingGeneration string                             `json:"incoming_generation"`
	NativeGeneration   string                             `json:"native_generation"`
	KernelBootID       string                             `json:"kernel_boot_id"`
	PID                int                                `json:"pid"`
	StartTime          uint64                             `json:"start_time"`
	TargetSHA256       string                             `json:"target_sha256"`
	CaptureSHA256      string                             `json:"capture_sha256"`
	BackingsSHA256     string                             `json:"backings_sha256"`
	Cgroup             nativeHostHelperGroup              `json:"cgroup"`
	Images             [5]nativeSnapshotBackingImage      `json:"images"`
	Phases             [nativeRestorePhaseCount]time.Time `json:"phases"`
}

func (r *nativeQualificationRestoreLoadRecord) UnmarshalJSON(body []byte) error {
	fields, err := nativeJournalObjectFields(body, nativeQualificationJSONFields(reflect.TypeOf(*r)))
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["cgroup"], []string{"path", "device", "inode"}); err != nil {
		return err
	}
	var images, phases []json.RawMessage
	if err := json.Unmarshal(fields["images"], &images); err != nil || len(images) != len(r.Images) {
		return errors.Join(err, errors.New("native restore load: exactly five image witnesses are required"))
	}
	if err := json.Unmarshal(fields["phases"], &phases); err != nil || len(phases) != len(r.Phases) {
		return errors.Join(err, errors.New("native restore load: exact effect phases are required"))
	}
	for _, phase := range phases {
		if bytes.Equal(bytes.TrimSpace(phase), []byte("null")) {
			return errors.New("native restore load: null effect phase")
		}
	}
	type plain nativeQualificationRestoreLoadRecord
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	return d.Decode((*plain)(r))
}

func nativeRestoreEvidenceHash(value any) string {
	// These private journal values contain no unsupported JSON field types.
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func nativeRestoreTargetHash(r nativeQualificationRestoreRecord) string {
	r.Revoked = false // Revocation retains the original effect evidence.
	return nativeRestoreEvidenceHash(r)
}

func (r nativeQualificationRestoreLoadRecord) validate() error {
	if r.Version != 1 || !canonicalNativeHelperID(r.InstanceID) || !canonicalNativeHelperID(r.IncomingGeneration) ||
		!canonicalNativeHelperID(r.NativeGeneration) || !canonicalNativeHelperID(r.KernelBootID) || r.PID <= 0 || r.StartTime == 0 ||
		r.IncomingGeneration == r.NativeGeneration || r.Phases[0].IsZero() || r.Cgroup.Device == 0 || r.Cgroup.Inode == 0 ||
		r.Cgroup.Path == "" || filepath.IsAbs(r.Cgroup.Path) || filepath.Clean(r.Cgroup.Path) != r.Cgroup.Path || strings.HasPrefix(r.Cgroup.Path, "..") {
		return errors.New("native restore load: original target process or effect intent is incomplete")
	}
	for _, digest := range []string{r.TargetSHA256, r.CaptureSHA256, r.BackingsSHA256} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != digest {
			return errors.New("native restore load: original evidence digest is invalid")
		}
	}
	previous := r.Phases[0]
	ended := false
	for _, phase := range r.Phases[1:] {
		if phase.IsZero() {
			ended = true
			continue
		}
		if ended || phase.Before(previous) {
			return errors.New("native restore load: skipped or reversed effect phase")
		}
		previous = phase
	}
	for i, image := range r.Images {
		if err := image.validateContentIdentity(); err != nil {
			return err
		}
		if i < 2 && !nativeSnapshotBackingName(image.Name) || i >= 2 && image.Name != [...]string{memSnapshotName, vmstateSnapshotName, layerImageName}[i-2] {
			return errors.New("native restore load: original input name changed")
		}
		for _, old := range r.Images[:i] {
			if image.Name == old.Name || image.Epoch == old.Epoch || image.ReferenceID == old.ReferenceID || image.Identity == old.Identity {
				return errors.New("native restore load: target image witnesses are aliased")
			}
		}
	}
	return nil
}

func (r nativeQualificationRestoreLoadRecord) requireOriginal(target nativeQualificationRestoreRecord, capture nativeQualificationCaptureRecord, backings nativeSnapshotBackingRecord, physical nativeLaunchRecord) error {
	if err := errors.Join(r.validate(), backings.validate(capture)); err != nil {
		return err
	}
	if r.InstanceID != target.Execution.InstanceID || r.IncomingGeneration != target.Generation || r.NativeGeneration != target.NativeGeneration ||
		r.KernelBootID != target.KernelBootID || r.NativeGeneration != physical.Generation || r.KernelBootID != physical.KernelBootID ||
		r.PID != physical.PID || r.StartTime != physical.StartTime || !sameNativePhysicalLease(physical.Lease, target.NativeLease) ||
		r.Cgroup.Path != nativeSnapshotMemoryPath(physical) || physical.Lease.MemoryMaxMiB != target.Execution.RAMMB ||
		r.TargetSHA256 != nativeRestoreTargetHash(target) || r.CaptureSHA256 != nativeRestoreEvidenceHash(capture) || r.BackingsSHA256 != nativeRestoreEvidenceHash(backings) ||
		r.Phases[0].Before(target.AcceptedAt) || r.Images[2].LogicalBytes != int64(target.Execution.RAMMB)<<20 ||
		r.Images[2].LogicalBytes != capture.Info.MemBytes || r.Images[3].LogicalBytes != capture.Info.VMStateBytes {
		return errors.New("native restore load: effect evidence lost the original target or capture")
	}
	for _, phase := range r.Phases {
		if !phase.IsZero() && phase.After(target.Deadline) {
			return errors.New("native restore load: effect evidence exceeds original target deadline")
		}
	}
	for i, original := range backings.Images {
		image := r.Images[i]
		if image.Name != original.Name || image.SHA256 != original.SHA256 || image.LogicalBytes != original.LogicalBytes ||
			image.Epoch == original.Epoch || image.ReferenceID == original.ReferenceID || image.Identity == original.Identity {
			return errors.New("native restore load: target borrowed or changed captured backing ownership")
		}
	}
	return nil
}

type nativeQualificationRestoreLoadJournal struct {
	restores   *nativeQualificationRestoreJournal
	writeValue func(string, nativeQualificationRestoreLoadRecord) error
}

func (j *nativeQualificationRestoreJournal) loads() *nativeQualificationRestoreLoadJournal {
	return &nativeQualificationRestoreLoadJournal{restores: j}
}
func (j *nativeQualificationRestoreLoadJournal) root() string {
	return filepath.Join(j.restores.root(), "loads")
}
func (j *nativeQualificationRestoreLoadJournal) path(instance string) (string, error) {
	key, err := j.restores.incoming.key(instance)
	return filepath.Join(j.root(), key+".json"), err
}
func (j *nativeQualificationRestoreLoadJournal) read(instance string) (r nativeQualificationRestoreLoadRecord, result error) {
	path, err := j.path(instance)
	if err != nil {
		return r, err
	}
	if err := errors.Join(checkNativeJournalPath(j.root(), true), checkNativeJournalPath(path, false)); err != nil {
		return r, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return r, err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	body, err := io.ReadAll(io.LimitReader(file, api.NativeQualificationRestoreLoadRecordMaxBytes+1))
	if err != nil || len(body) > api.NativeQualificationRestoreLoadRecordMaxBytes {
		return r, errors.Join(err, errors.New("native restore load: effect record exceeds parser bound"))
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return r, err
	}
	key, _ := j.restores.incoming.key(r.InstanceID)
	want, _ := j.restores.incoming.key(instance)
	if key != want {
		return r, errors.New("native restore load: effect record belongs to another target")
	}
	return r, r.validate()
}
func (j *nativeQualificationRestoreLoadJournal) write(r nativeQualificationRestoreLoadRecord) error {
	if err := r.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(j.root(), 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		return err
	}
	path, err := j.path(r.InstanceID)
	if err != nil {
		return err
	}
	if j.writeValue != nil {
		return j.writeValue(path, r)
	}
	return writeNativeJournalValue(path, r)
}

// The target incoming and physical locks span begin, each effect and advance.
func (j *nativeQualificationRestoreLoadJournal) begin(r nativeQualificationRestoreLoadRecord) error {
	path, err := j.path(r.InstanceID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("native restore load: original target already attempted an effect"))
	}
	for _, phase := range r.Phases[1:] {
		if !phase.IsZero() {
			return errors.New("native restore load: begin cannot adopt an acknowledgement")
		}
	}
	return j.write(r)
}
func (j *nativeQualificationRestoreLoadJournal) advance(r nativeQualificationRestoreLoadRecord, step int, now time.Time) (nativeQualificationRestoreLoadRecord, error) {
	original, err := j.read(r.InstanceID)
	if err != nil || original != r || step <= 0 || step >= len(r.Phases) || !r.Phases[step].IsZero() || r.Phases[step-1].IsZero() || now.IsZero() || now.Before(r.Phases[step-1]) {
		return r, errors.Join(err, errors.New("native restore load: original effect cannot advance or replay"))
	}
	r.Phases[step] = now.UTC()
	return r, j.write(r)
}

func (j *nativeQualificationRestoreLoadJournal) validateInventory(ctx context.Context) error {
	if err := checkNativeJournalPath(j.root(), true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(j.root())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".launch-") && entry.Type().IsRegular() {
			continue
		}
		instance, ok := strings.CutSuffix(entry.Name(), ".json")
		key, err := j.restores.incoming.key(instance)
		if !ok || err != nil || key != instance || !entry.Type().IsRegular() {
			return errors.New("native restore load: unexpected effect evidence entry")
		}
		if err := j.validateTarget(ctx, instance); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (j *nativeQualificationRestoreLoadJournal) validateTarget(ctx context.Context, instance string) (result error) {
	q := j.restores.incoming
	lock, err := q.lock(ctx, instance)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	r, err := j.read(instance)
	if err != nil {
		return err
	}
	target, err := j.restores.read(instance)
	if err != nil {
		return err
	}
	capture, err := j.restores.requireCapture(ctx, target)
	if err != nil {
		return err
	}
	backings, err := q.readBackings(capture)
	if err != nil {
		return err
	}
	physical, err := q.owner.read(instance)
	if err != nil {
		return err
	}
	if err := r.requireOriginal(target, capture, backings, physical); err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: q.owner}
	return images.requireRestoreLoadWitnesses(r, physical, "", true)
}
