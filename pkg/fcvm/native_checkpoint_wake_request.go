// adr: 590
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/state"
)

func validCheckpointWakeDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

// The commitment contains no environment values or ciphertext. JSON encoding
// covers every exported ordinary WakeRequest field, including nested artifacts
// and policy. Internal execution credentials and prepared secret files must be
// absent at this pre-effects boundary.
func checkpointWakeRequestDigest(frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease, req WakeRequest) (string, error) {
	if err := validateCheckpointWakeFrame(frame, frame.NodeID); err != nil {
		return "", err
	}
	if !checkpointWakeValidStrings(reflect.ValueOf(req)) {
		return "", errors.New("native checkpoint wake: invalid UTF-8 in original request")
	}
	if err := validateCheckpointWakeLease(frame, lease); err != nil {
		return "", err
	}
	if req.Instance != frame.InstanceID || req.AppID != frame.AppID || req.DeploymentID != frame.DeploymentID || req.Plan != lease.Plan || req.ExecutionOnly || req.AppTaskOnly || req.KeepPaused || req.ExportDir != "" || req.BuildTimeoutSec != 0 || req.ExecutionLeaseToken != "" || len(req.ExecutionOutboundIntegrationIDs) != 0 || len(req.preparedSecretsEnvJSON) != 0 || len(req.preparedAPIEnvJSON) != 0 {
		return "", errors.New("native checkpoint wake: request differs from original ordinary workload")
	}
	if req.MemSizeMiB <= 0 || req.VcpuCount <= 0 || req.BaseKey == "" || req.LayerKey == "" || lease.MemoryMaxMiB != wakeGuestMemoryMiB(req) || lease.CPUMillicores != req.CPUMillicores || lease.DisableStartupCPUBoost != req.DisableStartupCPUBoost || lease.BuildTimeoutSec != 0 {
		return "", errors.New("native checkpoint wake: request differs from physical resource binding")
	}
	if (frame.Method == "cold_boot") != (req.Snapshot == nil) || (req.Snapshot != nil && (req.Snapshot.DeploymentID != frame.DeploymentID || req.Snapshot.Networkless || req.Snapshot.Stale || req.Snapshot.StorageKey == "" || req.Snapshot.FCVersion == "" || (req.Snapshot.VMStatePath == "" && req.Snapshot.VMStateStorageKey == ""))) {
		return "", errors.New("native checkpoint wake: snapshot differs from original dispatch method")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("native checkpoint wake: encode request commitment: %w", err)
	}
	digest := sha256.Sum256(append([]byte("gregale-checkpoint-wake-request-v1\x00"), data...))
	return hex.EncodeToString(digest[:]), nil
}

// PrepareCheckpointWakeRequest binds the full original ordinary request before
// physical publication. Its result grants no producer or serving authority.
func (m *Manager) PrepareCheckpointWakeRequest(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease, req WakeRequest) (NativeCheckpointWakeOwnership, error) {
	j, err := m.checkpointWakeJournal(frame)
	if err != nil {
		return NativeCheckpointWakeOwnership{}, err
	}
	digest, err := checkpointWakeRequestDigest(frame, lease, req)
	if err != nil {
		return NativeCheckpointWakeOwnership{}, err
	}
	record, err := j.preparePhysicalDigest(ctx, frame, lease, digest)
	if err != nil {
		return NativeCheckpointWakeOwnership{}, fmt.Errorf("native checkpoint wake: bind original request: %w", err)
	}
	return NativeCheckpointWakeOwnership{Dispatch: record.Dispatch, IncomingGeneration: record.Generation, KernelBootID: record.KernelBootID, NativeGeneration: record.NativeGeneration, Claimed: record.Claimed, Revoked: record.Revoked}, nil
}

// matchRequest requires durable original ownership and exact physical identity.
// It is read-only and deliberately supplies no launch permit.
func (r nativeCheckpointWakeRecord) matchRequest(frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease, req WakeRequest) error {
	if err := r.validate(frame.NodeID); err != nil {
		return err
	}
	frame.DispatchedAt = frame.DispatchedAt.UTC().Round(0)
	if r.Dispatch != frame || !r.Claimed || r.Revoked || r.NativeGeneration == "" || r.RequestDigest == "" || !sameNativePhysicalLease(r.NativeLease, lease) {
		return errors.New("native checkpoint wake: original request ownership unavailable")
	}
	digest, err := checkpointWakeRequestDigest(frame, lease, req)
	if err != nil {
		return err
	}
	if digest != r.RequestDigest {
		return errors.New("native checkpoint wake: retained request changed")
	}
	return nil
}

// encoding/json replaces invalid UTF-8. Reject it before hashing so distinct
// invalid input cannot acquire the same commitment as a replacement character.
func checkpointWakeValidStrings(value reflect.Value) bool {
	if value.Type() == reflect.TypeOf(json.RawMessage{}) {
		return utf8.Valid(value.Bytes())
	}
	switch value.Kind() {
	case reflect.String:
		return utf8.ValidString(value.String())
	case reflect.Pointer, reflect.Interface:
		return value.IsNil() || checkpointWakeValidStrings(value.Elem())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() && !checkpointWakeValidStrings(value.Field(i)) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if !checkpointWakeValidStrings(value.Index(i)) {
				return false
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if !checkpointWakeValidStrings(iter.Key()) || !checkpointWakeValidStrings(iter.Value()) {
				return false
			}
		}
	}
	return true
}

// CheckCheckpointWakeRequest compares incoming ownership under its shared lock.
// It grants no physical-publication, launch, cleanup or closure authority.
func (m *Manager) CheckCheckpointWakeRequest(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease, req WakeRequest) (err error) {
	j, err := m.checkpointWakeJournal(frame)
	if err != nil {
		return err
	}
	lock, err := j.owner.qualifications(j.nodeID).lock(ctx, frame.InstanceID)
	if err != nil {
		return fmt.Errorf("native checkpoint wake: lock request observation: %w", err)
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	if err = ctx.Err(); err != nil {
		return err
	}
	record, err := j.read(frame.InstanceID)
	if err != nil {
		return fmt.Errorf("native checkpoint wake: read request ownership: %w", err)
	}
	return record.matchRequest(frame, lease, req)
}
