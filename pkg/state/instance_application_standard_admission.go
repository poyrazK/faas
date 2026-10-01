package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

var ErrApplicationStandardRuntimeStale = errors.New("state: runtime inputs changed after admission")
var ErrApplicationStandardRuntimeBusy = errors.New("state: runtime admission inputs are busy")

// InstanceApplicationStandardAdmission is a durable capture of control-plane
// inputs. It does not assert that vmmd enforced them or that consumers observed
// them. The private snapshot contains hashes in place of log URLs/credentials.
type InstanceApplicationStandardAdmission struct {
	InstanceID        string
	AppID             string
	DeploymentID      string
	DesiredRevision   int64
	PersistedRevision int64
	EffectiveHash     string
	InputHash         string
	NativeInputHash   string
	NodeID            string
	AccountID         string
	EgressRevision    int64
	Managed           bool
	CapturedAt        time.Time
	inputs            json.RawMessage
}

type InstanceApplicationStandardAdmissionStore interface {
	GetInstanceApplicationStandardAdmission(context.Context, string) (InstanceApplicationStandardAdmission, error)
}

// CheckInstanceApplicationStandardAdmission rejects a scheduler's stale cached
// app/account/deployment read, even if the later instance insert captured newer
// inputs. The publication guard independently checks current persisted inputs.
// This check and that guard do not replace a native, revision-bound boot receipt.
func CheckInstanceApplicationStandardAdmission(ctx context.Context, store InstanceApplicationStandardAdmissionStore, id string, app App, account Account, deployment Deployment) error {
	if !sameStandardUUID(account.ID, app.AccountID) || deployment.ID != "" && !sameStandardUUID(deployment.AppID, app.ID) {
		return ErrApplicationStandardRuntimeStale
	}
	capture, err := store.GetInstanceApplicationStandardAdmission(ctx, id)
	if err != nil {
		return fmt.Errorf("read runtime admission: %w", err)
	}
	reader, ok := store.(interface {
		GetApplicationStandardEnrollment(context.Context, string, string) (ApplicationStandardEnrollment, error)
	})
	if !ok {
		return fmt.Errorf("runtime enrollment reader unavailable")
	}
	enrollment, err := reader.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if err != nil {
		return fmt.Errorf("read captured runtime enrollment: %w", err)
	}
	if !ApplicationStandardEnrollmentPermitsRuntime(app, enrollment) {
		return ErrApplicationStandardsPending
	}
	if capture.DesiredRevision != enrollment.DesiredRevision || capture.PersistedRevision != enrollment.PersistedRevision || capture.EffectiveHash != enrollment.EffectiveHash {
		return ErrApplicationStandardRuntimeStale
	}
	var actual map[string]json.RawMessage
	if err := json.Unmarshal(capture.inputs, &actual); err != nil {
		return fmt.Errorf("decode runtime admission: %w", err)
	}
	var artifact map[string]json.RawMessage
	if json.Unmarshal(actual["artifact"], &artifact) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	delete(artifact, "sidecars") // checked by the durable guard; resolved separately by schedd
	actual["artifact"], err = json.Marshal(artifact)
	if err != nil {
		return err
	}
	expected := standardRuntimeCallerInputs(app, account, deployment)
	for field, value := range expected {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var want, got any
		if json.Unmarshal(raw, &want) != nil || json.Unmarshal(actual[field], &got) != nil || !reflect.DeepEqual(want, got) {
			return fmt.Errorf("%w: %s", ErrApplicationStandardRuntimeStale, field)
		}
	}
	return nil
}

func standardRuntimeCallerInputs(app App, account Account, dep Deployment) map[string]any {
	projectID := ""
	if app.ProjectID != "" {
		projectID = canonicalStandardUUID(app.ProjectID)
	}
	artifact := map[string]any{}
	if dep.ID != "" {
		artifact = standardRuntimeArtifact(dep)
	}
	return map[string]any{
		"app_id": canonicalStandardUUID(app.ID), "org_id": canonicalStandardUUID(app.OrgID), "project_id": projectID,
		"account_id": canonicalStandardUUID(app.AccountID), "account_plan": account.Plan, "account_egress_allowlist_extra": account.EgressAllowlistExtra,
		"settings": applicationStandardBaseSettings(app),
		"runtime":  map[string]any{"type": app.Type, "runtime": app.Runtime, "ram_mb": app.RAMMB, "app_protocol": app.AppProtocol},
		"artifact": artifact,
	}
}

func standardRuntimeArtifact(dep Deployment) map[string]any {
	var scanStatus any
	if dep.ScanStatus != "" {
		scanStatus = dep.ScanStatus
	}
	return map[string]any{"id": canonicalStandardUUID(dep.ID), "scope": dep.Scope, "kind": dep.Kind,
		"image_digest": dep.ImageDigest, "rootfs_key": dep.RootfsKey, "rootfs_path": dep.RootfsPath, "rootfs_bytes": dep.RootfsBytes,
		"source_sha256": dep.SourceSHA256, "parked_reason": dep.ParkedReason, "scan_status": scanStatus,
		"scan_result_hash": standardReviewBytesDigest(dep.ScanResult)}
}

func decodeInstanceStandardAdmission(id string, raw []byte, capturedAt time.Time) (InstanceApplicationStandardAdmission, error) {
	var input struct {
		AppID              string            `json:"app_id"`
		AccountID          string            `json:"account_id"`
		EgressRevision     int64             `json:"egress_revision"`
		Adoptions          []json.RawMessage `json:"adoptions"`
		MaterializedFields []string          `json:"materialized_fields"`
		Artifact           struct {
			ID string `json:"id"`
		} `json:"artifact"`
		DesiredRevision   int64  `json:"desired_revision"`
		PersistedRevision int64  `json:"persisted_revision"`
		EffectiveHash     string `json:"effective_hash"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return InstanceApplicationStandardAdmission{}, err
	}
	// Decode before hashing so PostgreSQL JSON spacing/key order cannot change
	// the opaque digest returned by this internal store API.
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&canonical); err != nil {
		return InstanceApplicationStandardAdmission{}, err
	}
	hash, err := standardReviewDigest(canonical)
	if err != nil {
		return InstanceApplicationStandardAdmission{}, err
	}
	return InstanceApplicationStandardAdmission{InstanceID: id, AppID: input.AppID, DeploymentID: input.Artifact.ID,
		AccountID: input.AccountID, EgressRevision: input.EgressRevision, Managed: len(input.Adoptions) > 0 || len(input.MaterializedFields) > 0,
		DesiredRevision: input.DesiredRevision, PersistedRevision: input.PersistedRevision, EffectiveHash: input.EffectiveHash,
		InputHash: hash, CapturedAt: capturedAt, inputs: append(json.RawMessage(nil), raw...)}, nil
}

func standardRuntimeAdmissionState(s string) bool {
	switch State(s) {
	case StateWaking, StateColdBooting, StateRunning, StateWarm, StateMigrating:
		return true
	}
	return false
}
