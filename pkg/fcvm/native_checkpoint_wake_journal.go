// adr: 590
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wakeownershipwire"
)

// Incoming binding records planned physical ownership, never launch or closure
// authority. Serving and native producer adapters remain gated.
type nativeCheckpointWakeRecord struct {
	RequestDigest    string                                    `json:"request_digest"`
	Version          int                                       `json:"version"`
	Generation       string                                    `json:"generation"`
	KernelBootID     string                                    `json:"kernel_boot_id"`
	Dispatch         state.ProjectEnvironmentCloneWakeDispatch `json:"dispatch"`
	AcceptedAt       time.Time                                 `json:"accepted_at"`
	Claimed          bool                                      `json:"claimed"`
	NativeGeneration string                                    `json:"native_generation"`
	NativeLease      Lease                                     `json:"native_lease"`
	Revoked          bool                                      `json:"revoked"`
}

func (r *nativeCheckpointWakeRecord) UnmarshalJSON(data []byte) error {
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return err
	}
	names := make([]string, 0)
	for _, name := range nativeQualificationJSONFields(reflect.TypeOf(*r)) {
		if header.Version < 3 && name == "request_digest" {
			continue
		}
		if header.Version == 1 && (name == "native_generation" || name == "native_lease") {
			continue
		}
		names = append(names, name)
	}
	fields, err := nativeJournalObjectFields(data, names)
	if err != nil {
		return err
	}
	if _, err = nativeJournalObjectFields(fields["dispatch"], []string{"InstanceID", "AppID", "DeploymentID", "NodeID", "WakeID", "RequestReason", "Method", "DispatchedAt"}); err != nil {
		return err
	}
	if header.Version != 1 {
		if _, err := nativeJournalObjectFields(fields["native_lease"], nativeJournalLeaseFields()); err != nil {
			return err
		}
	}
	type plain nativeCheckpointWakeRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(r))
}

func validateCheckpointWakeFrame(frame state.ProjectEnvironmentCloneWakeDispatch, node string) error {
	if frame.NodeID != node {
		return fmt.Errorf("native checkpoint wake: foreign node: %w", state.ErrConflict)
	}
	if _, err := wakeownershipwire.Outgoing(context.Background(), frame); err != nil {
		return fmt.Errorf("native checkpoint wake: invalid original dispatch: %w", err)
	}
	return nil
}

func (r nativeCheckpointWakeRecord) validate(node string) error {
	if err := validateCheckpointWakeFrame(r.Dispatch, node); err != nil {
		return err
	}
	if (r.Version != 1 && r.Version != 2 && r.Version != 3) || !canonicalNativeHelperID(r.Generation) || !canonicalNativeHelperID(r.KernelBootID) || r.AcceptedAt.IsZero() || (!r.Claimed && !r.Revoked) {
		return errors.New("native checkpoint wake: invalid incoming record")
	}
	if r.RequestDigest != "" && (r.Version != 3 || r.NativeGeneration == "" || !validCheckpointWakeDigest(r.RequestDigest)) {
		return errors.New("native checkpoint wake: invalid request commitment")
	}
	if r.NativeGeneration == "" {
		if r.NativeLease != (Lease{}) {
			return errors.New("native checkpoint wake: unbound lease is present")
		}
	} else {
		if r.Version < 2 || !r.Claimed || !canonicalNativeHelperID(r.NativeGeneration) || r.NativeGeneration == r.Generation {
			return errors.New("native checkpoint wake: invalid physical generation binding")
		}
		if err := validateCheckpointWakeLease(r.Dispatch, r.NativeLease); err != nil {
			return err
		}
	}
	return nil
}

type nativeCheckpointWakeJournal struct {
	root, nodeID string
	owner        *nativeLaunchJournal
	writeValue   func(string, nativeCheckpointWakeRecord) error
}

func (j *nativeLaunchJournal) checkpointWakes(node string) *nativeCheckpointWakeJournal {
	return &nativeCheckpointWakeJournal{root: filepath.Join(j.root, "checkpoint-wakes"), nodeID: node, owner: j}
}

func (j *nativeCheckpointWakeJournal) path(instance string) (string, error) {
	key, err := j.owner.qualifications(j.nodeID).key(instance)
	return filepath.Join(j.root, key+".json"), err
}

func (j *nativeCheckpointWakeJournal) read(instance string) (out nativeCheckpointWakeRecord, err error) {
	defer func() {
		if err != nil {
			out = nativeCheckpointWakeRecord{}
		}
	}()
	path, err := j.path(instance)
	if err != nil {
		return out, err
	}
	if err = checkNativeJournalPath(j.root, true); err != nil {
		return out, err
	}
	if err = checkNativeJournalPath(path, false); err != nil {
		return out, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	decoder := json.NewDecoder(file)
	if err = decoder.Decode(&out); err != nil {
		return out, err
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return out, errors.New("native checkpoint wake: trailing record data")
	}
	err = nil
	if err = out.validate(j.nodeID); err != nil {
		return out, err
	}
	boot, err := j.owner.currentBootID()
	if err != nil {
		return out, err
	}
	key, err := j.path(out.Dispatch.InstanceID)
	if err != nil || key != path || out.KernelBootID != boot {
		return out, errors.Join(err, errors.New("native checkpoint wake: instance or kernel boot changed"))
	}
	return out, nil
}

func (j *nativeCheckpointWakeJournal) write(record nativeCheckpointWakeRecord) error {
	if record.Version != 3 {
		return errors.New("native checkpoint wake: legacy records must be upgraded before writing")
	}
	if err := record.validate(j.nodeID); err != nil {
		return err
	}
	if err := os.MkdirAll(j.root, 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(j.root, true); err != nil {
		return err
	}
	path, err := j.path(record.Dispatch.InstanceID)
	if err != nil {
		return err
	}
	if j.writeValue != nil {
		return j.writeValue(path, record)
	}
	return writeNativeJournalValue(path, record)
}

// The shared incoming lock excludes qualification claims and generic native
// preparation while the exact original tombstone or claim is published.
func (j *nativeCheckpointWakeJournal) update(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch, claim bool) (out nativeCheckpointWakeRecord, err error) {
	defer func() {
		if err != nil {
			out = nativeCheckpointWakeRecord{}
		}
	}()
	if j.owner == nil {
		return out, errors.New("native checkpoint wake: owner unavailable")
	}
	if err = validateCheckpointWakeFrame(frame, j.nodeID); err != nil {
		return out, err
	}
	frame.DispatchedAt = frame.DispatchedAt.UTC().Round(0)
	q := j.owner.qualifications(j.nodeID)
	lock, err := q.lock(ctx, frame.InstanceID)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	out, err = j.read(frame.InstanceID)
	if err == nil {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if out.Dispatch != frame || claim {
			return out, errors.New("native checkpoint wake: incoming request already exists or changed")
		}
		if out.Revoked {
			return out, nil
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// Another incoming owner cannot share this instance, even without a VM.
		if err = q.requireProfileAbsent(frame.InstanceID, true); err != nil {
			return out, err
		}
		qualificationPath, pathErr := q.path(frame.InstanceID)
		if pathErr != nil {
			return out, pathErr
		}
		if _, pathErr = os.Lstat(qualificationPath); !errors.Is(pathErr, os.ErrNotExist) {
			return out, errors.Join(pathErr, errors.New("native checkpoint wake: qualification owner exists"))
		}
		if err = q.requireNativeAbsent(frame.InstanceID); err != nil {
			return out, err
		}
		boot, bootErr := j.owner.currentBootID()
		if bootErr != nil {
			return out, bootErr
		}
		out = nativeCheckpointWakeRecord{Version: 3, Generation: uuid.NewString(), KernelBootID: boot, Dispatch: frame, AcceptedAt: time.Now().UTC()}
	} else {
		return out, err
	}
	out.Version = 3
	out.Claimed = out.Claimed || claim
	out.Revoked = out.Revoked || !claim
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if err = j.write(out); err != nil {
		return out, err
	}
	return out, nil
}

func (j *nativeCheckpointWakeJournal) claim(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch) (nativeCheckpointWakeRecord, error) {
	return j.update(ctx, frame, true)
}
func (j *nativeCheckpointWakeJournal) revoke(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch) (nativeCheckpointWakeRecord, error) {
	return j.update(ctx, frame, false)
}

// The caller holds the shared incoming lock. Any retained or damaged incoming
// record prevents a generic producer from bypassing future original authority.
func (j *nativeLaunchJournal) requireCheckpointWakeAbsent(instance string) error {
	w := j.checkpointWakes("")
	if _, err := os.Lstat(w.root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := checkNativeJournalPath(w.root, true); err != nil {
		return err
	}
	path, err := w.path(instance)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("native checkpoint wake: retained incoming ownership requires a native binding protocol")
}
