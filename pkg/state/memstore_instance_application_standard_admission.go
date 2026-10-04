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
	copy, err := decodeInstanceStandardAdmission(id, capture.inputs, capture.CapturedAt)
	copy.NodeID, copy.NativeInputHash = capture.NodeID, capture.NativeInputHash
	return copy, err
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
	if e.ExceptionExpiresAt != nil {
		input["exception_expires_at_unix_nano"] = e.ExceptionExpiresAt.UnixNano()
	}
	input["egress_revision"] = m.appEgressRevisionLocked(app.ID)
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
		identity, err := m.runtimeArtifactIdentityLocked(app, dep)
		if err != nil {
			return nil, err
		}
		if identity != nil {
			input["runtime_artifacts"] = identity
			removeStandardApprovalMirrors(input["artifact"].(map[string]any))
		}
	}
	return json.Marshal(input)
}

// Called under m.mu before the instance mutation. Unowned legacy apps retain
// residency compatibility without obtaining company or native grant authority.
func (m *MemStore) guardInstanceStandardRuntimeLocked(ins Instance, creating bool) error {
	app, exists := m.apps[ins.AppID]
	if !exists || ins.Kind == "job_task" || ins.Kind == "build" || !standardRuntimeAdmissionState(ins.State) {
		return nil
	}
	if app.OrgID == "" {
		return m.guardUnownedStandardRuntimeLocked(app)
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
		capture.NodeID = ins.NodeID
		capture.NativeInputHash = memNativeCaptureHash(input, ins.NodeID)
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
		matches, err := standardNativeRuntimeInputsMatch(capture.inputs, input)
		old := m.instances[ins.ID]
		if err == nil && !matches && old.State != string(StateWaking) && old.State != string(StateColdBooting) {
			matches, err = standardRuntimeInputsMatch(capture.inputs, input)
		}
		if err != nil || !matches {
			return ErrApplicationStandardRuntimeStale
		}
	}
	if capture.Managed && capture.ArtifactInputHash != "" {
		if _, err := m.standardNativeArtifactDeadlineLocked(capture); err != nil {
			return err
		}
	}
	if capture.Managed && (ins.State == string(StateRunning) || ins.State == string(StateWarm) || ins.State == string(StateMigrating)) {
		old := m.instances[ins.ID]
		if old.State != string(StateWaking) && old.State != string(StateColdBooting) && old.State != string(StateRunning) && old.State != string(StateWarm) && old.State != string(StateMigrating) {
			return ErrApplicationStandardRuntimeStale
		}
	}
	return m.guardNativeRuntimeReceiptLocked(ins, capture)
}

func (m *MemStore) guardUnownedStandardRuntimeLocked(app App) error {
	account, exists := m.accounts[app.AccountID]
	if app.Status == AppDeleted || !exists || !account.Active() {
		return ErrApplicationStandardsPending
	}
	if _, retained := m.applicationStandardEnrollments[app.ID]; retained {
		return ErrApplicationStandardsPending
	}
	if _, err := m.applicationStandardAdmissionPinsLocked(app); err != nil {
		return ErrApplicationStandardsPending
	}
	return nil
}
