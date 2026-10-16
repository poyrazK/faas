package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// Qualification is a scoped, point-in-time diagnostic, not a lease or authority
// to advance an application's observation. Other instances and consumers must
// independently qualify in the observer's own fenced transaction.
type InstanceApplicationStandardRuntimeQualification struct {
	OrgID             string    `json:"org_id"`
	AppID             string    `json:"app_id"`
	InstanceID        string    `json:"instance_id"`
	DeploymentID      string    `json:"deployment_id"`
	NodeID            string    `json:"node_id"`
	State             string    `json:"state"`
	DesiredRevision   int64     `json:"desired_revision"`
	EffectiveHash     string    `json:"effective_hash"`
	RosterFingerprint string    `json:"roster_fingerprint"`
	Qualified         bool      `json:"qualified"`
	Reason            string    `json:"reason"`
	CheckedAt         time.Time `json:"checked_at"`
	ApprovalExpiresAt time.Time `json:"approval_expires_at,omitempty"`
}

type InstanceApplicationStandardRuntimeQualificationStore interface {
	GetInstanceApplicationStandardRuntimeQualification(context.Context, string, string, string) (InstanceApplicationStandardRuntimeQualification, error)
}

func standardRuntimeQualification(r ApplicationStandardConsumerRoster, id string) (InstanceApplicationStandardRuntimeQualification, error) {
	for _, i := range r.LiveInstances {
		if !sameStandardUUID(i.InstanceID, id) {
			continue
		}
		q := InstanceApplicationStandardRuntimeQualification{OrgID: r.OrgID, AppID: r.AppID, InstanceID: i.InstanceID, DeploymentID: i.DeploymentID, NodeID: i.NodeID, State: i.State, DesiredRevision: r.DesiredRevision, EffectiveHash: r.EffectiveHash, RosterFingerprint: r.Fingerprint(), CheckedAt: r.ReadAt}
		if !r.EnrollmentCurrent || r.DesiredRevision < 1 || r.PersistedRevision != r.DesiredRevision {
			q.Reason = "enrollment_pending"
		} else if i.State != string(StateRunning) && i.State != string(StateSnapshotting) && i.State != string(StateMigrating) {
			q.Reason = "runtime_transition_pending"
		} else if !standardRuntimeQualificationNode(r, i.NodeID) {
			q.Reason = "native_consumer_unavailable"
		}
		return q, nil
	}
	return InstanceApplicationStandardRuntimeQualification{}, ErrNotFound
}

func standardRuntimeQualificationNode(r ApplicationStandardConsumerRoster, id string) bool {
	for _, n := range r.Nodes {
		if n.NodeID == id {
			return n.Present && n.Active && n.Role != "control-plane" && n.HeartbeatFresh && n.NativeRequired && n.NativeIncarnation != "" && n.NativeProtocol == runtimeadmission.ArtifactProtocolVersion
		}
	}
	return false
}

func standardRuntimeQualificationInputs(q InstanceApplicationStandardRuntimeQualification, c InstanceApplicationStandardAdmission, r runtimeadmission.Receipt, current []byte) string {
	if !c.Managed || c.ArtifactInputHash == "" || r.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion {
		return "native_artifact_consumption_required"
	}
	b := r.Binding
	if !sameStandardUUID(c.AppID, q.AppID) || !sameStandardUUID(c.InstanceID, q.InstanceID) || !sameStandardUUID(c.DeploymentID, q.DeploymentID) || !sameStandardUUID(c.NodeID, q.NodeID) || c.DesiredRevision != q.DesiredRevision || c.PersistedRevision != q.DesiredRevision || c.EffectiveHash != q.EffectiveHash {
		return "runtime_inputs_stale"
	}
	if b.AppID != c.AppID || b.InstanceID != c.InstanceID || b.DeploymentID != c.DeploymentID || b.AccountID != c.AccountID || !sameStandardUUID(b.NodeID, c.NodeID) || b.DesiredRevision != c.DesiredRevision || b.EffectiveHash != c.EffectiveHash || b.CapturedInputHash != c.NativeInputHash || b.EgressRevision != c.EgressRevision {
		return "native_receipt_stale"
	}
	matches, err := standardNativeRuntimeInputsMatch(c.inputs, current)
	if err != nil || !matches {
		return "runtime_inputs_stale"
	}
	hash, err := standardCapturedArtifactSourceHash(c)
	if err != nil || hash != b.ArtifactSourcesHash || r.Check(b, time.Unix(0, r.CompletedAtUnixNano)) != nil {
		return "native_receipt_stale"
	}
	return ""
}

func standardRuntimeQualificationComplete(q InstanceApplicationStandardRuntimeQualification, deadline, now time.Time) InstanceApplicationStandardRuntimeQualification {
	q.CheckedAt = now.UTC()
	if deadline.IsZero() || !deadline.After(now) {
		q.Reason = "artifact_approval_stale"
		return q
	}
	q.Qualified, q.Reason, q.ApprovalExpiresAt = true, "", deadline.UTC()
	return q
}
