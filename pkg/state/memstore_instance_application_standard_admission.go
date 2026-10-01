package state

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

var _ InstanceApplicationStandardAdmissionStore = (*MemStore)(nil)

func (m *MemStore) GetInstanceApplicationStandardAdmission(_ context.Context, id string) (InstanceApplicationStandardAdmission, error) {
	if !validStandardResourceRead(id, id) {
		return InstanceApplicationStandardAdmission{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	capture, ok := m.instanceApplicationStandardAdmissions[id]
	if !ok {
		return InstanceApplicationStandardAdmission{}, ErrNotFound
	}
	return decodeInstanceStandardAdmission(id, capture.inputs, capture.CapturedAt)
}

func (m *MemStore) standardRuntimeSnapshotLocked(ins Instance) ([]byte, error) {
	app, exists := m.apps[ins.AppID]
	if !exists || app.Status == AppDeleted {
		return nil, ErrApplicationStandardsPending
	}
	e, exists := m.applicationStandardEnrollments[app.ID]
	if !exists || !ApplicationStandardEnrollmentPermitsRuntime(app, e) {
		return nil, ErrApplicationStandardsPending
	}
	org, exists := m.orgs[app.OrgID]
	if !exists || (org.Status != OrgStatusActive && org.Status != OrgStatusPastDue) || org.DeletedPending {
		return nil, ErrApplicationStandardsPending
	}
	account, exists := m.accounts[app.AccountID]
	if !exists || !account.Active() {
		return nil, ErrApplicationStandardsPending
	}
	var dep Deployment
	if ins.DeploymentID != "" {
		dep, exists = m.deployments[ins.DeploymentID]
		if !exists || !sameStandardUUID(dep.AppID, app.ID) {
			return nil, ErrInvalidArgument
		}
	}
	input := standardRuntimeCallerInputs(app, account, dep)
	input["desired_revision"], input["persisted_revision"], input["effective_hash"] = e.DesiredRevision, e.PersistedRevision, e.EffectiveHash
	input["adoptions"], input["materialized_fields"], input["effective"] = e.Adoptions, e.MaterializedFields, e.Effective
	input["base_settings"], input["local_settings"], input["additional_log_destinations"] = e.BaseSettings, e.LocalSettings, e.AdditionalLogDestinations
	mode := ins.Mode
	if mode == "" {
		mode = string(InstanceModeNormal)
	}
	input["instance_ram_mb"], input["instance_mode"] = ins.RAMMB, mode
	drains := []standardReviewDrain{}
	for _, d := range m.appLogDrains {
		if sameStandardUUID(d.AppID, app.ID) {
			drains = append(drains, standardReviewDrain{ID: canonicalStandardUUID(d.ID), Kind: string(d.Kind), Enabled: d.Enabled, TargetHash: standardReviewBytesDigest([]byte(d.TargetURL)), AuthHash: standardReviewBytesDigest(d.AuthHeaderSealed)})
		}
	}
	slices.SortFunc(drains, func(a, b standardReviewDrain) int { return strings.Compare(a.ID, b.ID) })
	signers := []standardReviewSigner{}
	for _, s := range m.trustedSigners {
		if sameStandardUUID(s.AppID, app.ID) {
			signers = append(signers, standardReviewSigner{Name: s.SignerName, Fingerprint: standardReviewBytesDigest(s.CosignPublicKey)})
		}
	}
	slices.SortFunc(signers, func(a, b standardReviewSigner) int { return strings.Compare(a.Name, b.Name) })
	input["drains"], input["signers"] = drains, signers
	if dep.ID != "" {
		layers := []map[string]any{}
		for _, l := range m.deploymentSidecarLayers {
			if sameStandardUUID(l.DeploymentID, dep.ID) {
				layers = append(layers, map[string]any{"sidecar_name": l.SidecarName, "storage_key": l.StorageKey, "bytes": l.Bytes, "content_digest": l.ContentDigest})
			}
		}
		slices.SortFunc(layers, func(a, b map[string]any) int {
			return strings.Compare(a["sidecar_name"].(string), b["sidecar_name"].(string))
		})
		input["artifact"].(map[string]any)["sidecars"] = layers
	}
	return json.Marshal(input)
}

// Called under m.mu before the instance mutation. Only test fixtures with no
// persisted organization retain the legacy permissive behaviour.
func (m *MemStore) guardInstanceStandardRuntimeLocked(ins Instance, creating bool) error {
	app, exists := m.apps[ins.AppID]
	if !exists || app.OrgID == "" || ins.Kind == "job_task" || ins.Kind == "build" || !standardRuntimeAdmissionState(ins.State) {
		return nil
	}
	input, err := m.standardRuntimeSnapshotLocked(ins)
	if err != nil {
		return err
	}
	e := m.applicationStandardEnrollments[app.ID]
	managed := len(e.Adoptions) > 0 || len(e.MaterializedFields) > 0
	if creating {
		if managed && ins.State != string(StateWaking) && ins.State != string(StateColdBooting) {
			return ErrApplicationStandardRuntimeStale
		}
		capture, err := decodeInstanceStandardAdmission(ins.ID, input, time.Now().UTC())
		if err != nil {
			return err
		}
		if m.instanceApplicationStandardAdmissions == nil {
			m.instanceApplicationStandardAdmissions = map[string]InstanceApplicationStandardAdmission{}
		}
		m.instanceApplicationStandardAdmissions[ins.ID] = capture
		return nil
	}
	capture, found := m.instanceApplicationStandardAdmissions[ins.ID]
	if !found {
		if managed {
			return ErrApplicationStandardRuntimeStale
		}
		return nil
	}
	if !bytes.Equal(capture.inputs, input) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
