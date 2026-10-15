package state

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func standardObservationArtifact(raw []byte, evidence DeploymentRuntimeScanEvidence) standardApplicationQualification {
	c, err := decodeInstanceStandardAdmission("", raw, evidence.CheckedAt)
	if err != nil || !c.Managed || c.ArtifactInputHash == "" {
		return standardApplicationQualification{reason: "artifact_approval_stale"}
	}
	until, err := standardNativeArtifactDeadline(c, evidence)
	if err != nil {
		return standardApplicationQualification{reason: "artifact_approval_stale"}
	}
	return standardApplicationQualification{until: until}
}

func decodeStandardObservationSnapshotHistory(r ApplicationStandardSnapshotCaptureRecord, grant, ack []byte) (ApplicationStandardSnapshotCaptureRecord, bool) {
	if json.Unmarshal(grant, &r.Grant) != nil || json.Unmarshal(ack, &r.Acknowledgment) != nil {
		return r, false
	}
	return r, true
}

func standardObservationSnapshot(r ApplicationStandardSnapshotCaptureRecord, current []byte, roster ApplicationStandardConsumerRoster) string {
	if len(r.inputs) == 0 || r.Acknowledgment == nil || standardSnapshotRecordValid(r) != nil {
		return "snapshot_observation_pending"
	}
	b := r.Grant.Parent.Binding
	if b.AppID != roster.AppID || b.AccountID != roster.AccountID || b.DesiredRevision != roster.DesiredRevision || b.EffectiveHash != roster.EffectiveHash || b.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion {
		return "snapshot_inputs_stale"
	}
	var captured, next map[string]json.RawMessage
	if json.Unmarshal(r.inputs, &captured) != nil || json.Unmarshal(current, &next) != nil {
		return "snapshot_inputs_stale"
	}
	// Preserve the original instance shape while comparing current application,
	// producer, publisher, policy and standard inputs. The historical hash stays intact.
	next["instance_ram_mb"], next["instance_mode"] = captured["instance_ram_mb"], captured["instance_mode"]
	raw, err := json.Marshal(next)
	if err != nil {
		return "snapshot_inputs_stale"
	}
	matches, err := standardNativeRuntimeInputsMatch(r.inputs, raw)
	if err != nil || !matches {
		return "snapshot_inputs_stale"
	}
	for _, n := range roster.Nodes {
		if n.NodeID == b.NodeID && n.Present && n.Active && n.HeartbeatFresh && n.Role != "control-plane" && n.NativeIncarnation == b.Incarnation && n.NativeProtocol == runtimeadmission.ArtifactProtocolVersion {
			return ""
		}
	}
	return "snapshot_producer_unavailable"
}

func standardRetainedArtifact(status string, live, snapshot bool) bool {
	return live || snapshot || status != "failed" && status != "superseded" && status != "cancelled"
}

func standardObservationEvidenceExpired(q standardApplicationQualification, now time.Time) bool {
	return q.reason == "" && (q.until.IsZero() || !q.until.After(now))
}
