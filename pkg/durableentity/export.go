// adr: 940
package durableentity

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// InvokeRestoreState acquires temporary private authority only for an existing
// committed entity. It invokes no guest and releases authority after completion.
func (m *Manager) InvokeRestoreState(ctx context.Context, id ID, owner, requestID string, expectedVersion uint64, exported StateExport) (Result, error) {
	return m.invokeRestoreState(ctx, id, owner, requestID, expectedVersion, exported, "", "", nil)
}

// InvokeValidatedRestoreState revalidates only first execution, never receipt replay.
func (m *Manager) InvokeValidatedRestoreState(ctx context.Context, id ID, owner, requestID string, expectedVersion uint64, exported StateExport, deploymentID string, validate func(context.Context, json.RawMessage) error) (Result, error) {
	if !validUUID(deploymentID) || validate == nil {
		return Result{}, ErrInvalid
	}
	return m.invokeRestoreState(ctx, id, owner, requestID, expectedVersion, exported, deploymentID, "", validate)
}

// InvokeIsolatedValidatedRestoreState binds the exact release validator bundle
// into the receipt fingerprint as well as the deployment identity.
func (m *Manager) InvokeIsolatedValidatedRestoreState(ctx context.Context, id ID, owner, requestID string, expectedVersion uint64, exported StateExport, deploymentID, bundleHash string, validate func(context.Context, json.RawMessage) error) (Result, error) {
	if !validUUID(deploymentID) || !validRecoveryRevision(bundleHash) || validate == nil {
		return Result{}, ErrInvalid
	}
	return m.invokeRestoreState(ctx, id, owner, requestID, expectedVersion, exported, deploymentID, bundleHash, validate)
}

func (m *Manager) invokeRestoreState(ctx context.Context, id ID, owner, requestID string, expectedVersion uint64, exported StateExport, deploymentID, bundleHash string, validate func(context.Context, json.RawMessage) error) (Result, error) {
	if exported.Entity != id || expectedVersion == 0 || !validIdentity(requestID) || IsAlarmRequestID(requestID) || exported.Format != 1 || exported.Version == 0 || !json.Valid(exported.Data) || exported.Checksum != exportChecksum(exported) {
		return Result{}, ErrInvalid
	}
	claim, err := m.acquire(ctx, id, owner, true)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DurableEntityReleaseTimeout)
		defer cancel()
		_ = m.Release(cleanup, claim)
	}()
	return m.restoreState(ctx, claim, requestID, expectedVersion, exported, deploymentID, bundleHash, validate)
}

// StateExport contains application data only, never private execution authority
// or delivery history. It is sensitive customer data, not an authentication token.
type StateExport struct {
	Format   int             `json:"format"`
	Entity   ID              `json:"entity"`
	Version  uint64          `json:"version"`
	Data     json.RawMessage `json:"data"`
	Checksum string          `json:"checksum"`
}

// ExportState reads one authenticated immutable committed snapshot without writes.
func (m *Manager) ExportState(ctx context.Context, id ID) (StateExport, error) {
	view, err := m.Read(ctx, id)
	if err != nil {
		return StateExport{}, err
	}
	if view.Version == 0 {
		return StateExport{}, ErrNotFound
	}
	out := StateExport{Format: 1, Entity: id, Version: view.Version, Data: view.Data}
	out.Checksum = exportChecksum(out)
	return out, nil
}

func exportChecksum(value StateExport) string {
	value.Checksum = ""
	body, _ := json.Marshal(value)
	return digest(body)
}

var ErrRestoreObsolete = errors.New("durable entity restore expected version is stale")

// RestoreState is a trusted engine operation under an existing private claim.
// A stable request ID journals the restore alongside its result. Replay precedes
// the expected-version check. Only application data changes; current alarms,
// receipts, outbox and retry reservations survive. No guest code is invoked.
func (m *Manager) RestoreState(ctx context.Context, claim Claim, requestID string, expectedVersion uint64, exported StateExport) (Result, error) {
	return m.restoreState(ctx, claim, requestID, expectedVersion, exported, "", "", nil)
}

func (m *Manager) restoreState(ctx context.Context, claim Claim, requestID string, expectedVersion uint64, exported StateExport, deploymentID, bundleHash string, validate func(context.Context, json.RawMessage) error) (Result, error) {
	if expectedVersion == 0 || exported.Format != 1 || exported.Entity != claim.ID || exported.Version == 0 || !json.Valid(exported.Data) || exported.Checksum != exportChecksum(exported) || IsAlarmRequestID(requestID) {
		return Result{}, ErrInvalid
	}
	if len(exported.Data) > api.MaxDurableEntitySnapshotBytes {
		return Result{}, exceeded("snapshot_bytes", api.MaxDurableEntitySnapshotBytes, len(exported.Data))
	}
	// Marshal before dispatch: this copies caller-owned data and binds identity
	// to the entire operation, including the expected target version.
	payload, err := json.Marshal(struct {
		Operation  string      `json:"operation"`
		Expected   uint64      `json:"expected_version"`
		Export     StateExport `json:"export"`
		Deployment string      `json:"validation_deployment_id,omitempty"`
		BundleHash string      `json:"validation_bundle_sha256,omitempty"`
	}{"restore-state-v1", expectedVersion, exported, deploymentID, bundleHash})
	if err != nil {
		return Result{}, ErrInvalid
	}
	var stable struct {
		Export StateExport `json:"export"`
	}
	if json.Unmarshal(payload, &stable) != nil {
		return Result{}, ErrInvalid
	}
	return m.execute(ctx, claim, Request{ID: requestID, Payload: payload}, func(ctx context.Context, view View) (Transition, error) {
		if view.Version != expectedVersion {
			return Transition{}, ErrRestoreObsolete
		}
		if validate != nil {
			if err := validate(ctx, append(json.RawMessage(nil), stable.Export.Data...)); err != nil {
				return Transition{}, err
			}
		}
		return Transition{Data: stable.Export.Data, Result: json.RawMessage(`{"restored":true}`), AlarmAt: view.AlarmAt}, nil
	}, true)
}
