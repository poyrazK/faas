package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// Start is durable before snapshot effects. An interrupted capture cannot
// overwrite immutable object keys. Only a complete entry replays its receipt;
// uncertainty requires retirement and a new qualification attempt.
type nativeQualificationCaptureRecord struct {
	Version          int             `json:"version"`
	InstanceID       string          `json:"instance_id"`
	CaptureID        string          `json:"capture_id"`
	NativeGeneration string          `json:"native_generation"`
	KernelBootID     string          `json:"kernel_boot_id"`
	StartedAt        time.Time       `json:"started_at"`
	CompletedAt      time.Time       `json:"completed_at"`
	Info             SnapshotInfo    `json:"info"`
	Backing          BackingIdentity `json:"backing"`
}

func (r *nativeQualificationCaptureRecord) UnmarshalJSON(data []byte) error {
	fields, err := nativeJournalObjectFields(data, nativeQualificationJSONFields(reflect.TypeOf(*r)))
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["info"], []string{"MemBytes", "VMStateBytes", "StoredBytes"}); err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["backing"], nativeQualificationJSONFields(reflect.TypeOf(r.Backing))); err != nil {
		return err
	}
	type plain nativeQualificationCaptureRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(r))
}

func (r nativeQualificationCaptureRecord) validate(incoming nativeQualificationRecord) error {
	if r.Version != 1 || r.InstanceID != incoming.Execution.InstanceID || r.CaptureID != incoming.Generation ||
		r.NativeGeneration == "" || r.NativeGeneration != incoming.NativeGeneration || r.KernelBootID != incoming.KernelBootID ||
		r.StartedAt.Before(incoming.AcceptedAt) || !r.StartedAt.Before(incoming.Deadline) {
		return errors.New("native qualification: capture differs from original incoming authority")
	}
	if r.CompletedAt.IsZero() {
		if r.Info != (SnapshotInfo{}) || r.Backing != (BackingIdentity{}) {
			return errors.New("native qualification: incomplete capture carries completion evidence")
		}
	} else if r.CompletedAt.Before(r.StartedAt) || !r.CompletedAt.Before(incoming.Deadline) ||
		r.Info.MemBytes <= 0 || r.Info.VMStateBytes <= 0 || r.Info.StoredBytes <= 0 || !r.Backing.complete() {
		return errors.New("native qualification: capture completion evidence is incomplete")
	}
	return nil
}

func (j *nativeQualificationJournal) capturePath(instance string) (string, error) {
	key, err := j.key(instance)
	return filepath.Join(j.root, "captures", key+".json"), err
}

func (j *nativeQualificationJournal) readCapture(incoming nativeQualificationRecord) (record nativeQualificationCaptureRecord, result error) {
	path, err := j.capturePath(incoming.Execution.InstanceID)
	if err != nil {
		return record, err
	}
	if err := checkNativeJournalPath(filepath.Dir(path), true); err != nil {
		return record, err
	}
	if err := checkNativeJournalPath(path, false); err != nil {
		return record, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return record, err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record, errors.New("native qualification: trailing capture data")
	}
	return record, record.validate(incoming)
}

func (j *nativeQualificationJournal) writeCapture(incoming nativeQualificationRecord, record nativeQualificationCaptureRecord) error {
	if err := record.validate(incoming); err != nil {
		return err
	}
	path, err := j.capturePath(incoming.Execution.InstanceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(filepath.Dir(path), true); err != nil {
		return err
	}
	return writeNativeJournalValue(path, record)
}

// Inventory validates identity; it never promotes an interrupted capture,
// resurrects a live VM, or releases a native reservation.
func (j *nativeQualificationJournal) validateCaptures(ctx context.Context) error {
	root := filepath.Join(j.root, "captures")
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
		if entry.Name() == "backings" && entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".launch-") {
			continue
		}
		instance, ok := strings.CutSuffix(entry.Name(), ".json")
		key, err := j.key(instance)
		if !ok || err != nil || key != instance || !entry.Type().IsRegular() {
			return errors.New("native qualification: unexpected capture journal entry")
		}
		lock, err := j.lock(ctx, instance)
		if err != nil {
			return err
		}
		incoming, err := j.read(instance)
		if err == nil {
			_, err = j.readCapture(incoming)
		}
		if err := errors.Join(err, lock.Close()); err != nil {
			return err
		}
	}
	return errors.Join(j.validateBackings(ctx), ctx.Err())
}
