package events

import "time"

// Recovery-timeline vocabulary (Workstream B, issue #1184). The
// constants are the canonical kind strings written to events.kind;
// the typed struct payloads below mirror the JSON shape one-for-one.
//
// Why a separate vocabulary from wake.go: the wake timeline tracks
// the per-wake boot/park lifecycle (queue → boot → ready → park).
// The recovery timeline tracks per-node + per-instance failure-mode
// events (drain → migrate → recreate → reactivate). Conflating the
// two would force the dashboard's filters into a single kind
// namespace; splitting keeps each timeline's audit query fast and
// its kind-prefix stable. Same precedent as wake.go (prefix `wake.`
// + instances.liveness_failed) and trigger.go (prefix `trigger.`).
const (
	// NodeDraining — apid handler received POST
	// /v1/compute-nodes/{name}/drain and flipped lifecycle to
	// 'draining'. Payload: {node_id, node_name, initiated_at,
	// operator_subject}. The first stop on the drain timeline;
	// downstream NodeDrained + InstanceMigrated / InstanceRecreated
	// follow as the recovery arbiter confirms completion.
	NodeDraining = "node.draining"
	// NodeDrained — drain completed cleanly. The recovery arbiter
	// stamps this once the NodeMarkDrainCompleted CAS lands
	// (lifecycle 'draining' → 'active' with zero live instances).
	// Payload: {node_id, node_name, initiated_at, completed_at,
	// drained_instance_count}.
	NodeDrained = "node.drained"
	// NodeFailed — heartbeat staleness gate flipped lifecycle to
	// 'unavailable'. Payload: {node_id, node_name, last_heartbeat_at,
	// failed_at}. The recovery arbiter's per-tick input set starts
	// here.
	NodeFailed = "node.failed"
	// NodeRecovered — recovery arbiter confirmed the node is back
	// healthy. Stamped after NodeMarkRecovered lands (lifecycle
	// 'recovering' → 'active' with last_recovery_outcome='succeeded').
	// Payload: {node_id, node_name, recovery_initiated_at,
	// recovered_at, migrated_count, recreated_count}.
	NodeRecovered = "node.recovered"
	// InstanceMigrated — live-migration Phase-4 ack landed
	// (destination owner confirmed). The instance row now sits on
	// the destination node; the source row is freed. Payload:
	// {instance_id, app_id, deployment_id, source_node_id,
	// dest_node_id, migrated_at, lease_id}.
	InstanceMigrated = "instance.migrated"
	// InstanceRecreated — Engine.RecreateInstance transitioned a
	// stranded RUNNING / COLD_BOOTING row to PARKED with
	// kind='recovery_recreate'. Ledger release atomic with the
	// transition. Payload: {instance_id, app_id, deployment_id,
	// node_id, recreated_at, reason}.
	InstanceRecreated = "instance.recreated"
	// InstanceFailed — ReconcileDeadNodeInstances declared an
	// instance row failed after the recovery sweep. Payload:
	// {instance_id, app_id, deployment_id, node_id, failed_at,
	// reason}. reason is the closed enum: 'liveness_lost',
	// 'reconcile_timeout', 'recreate_exhausted'.
	InstanceFailed = "instance.failed"
)

// TopicRecovery is the SSE topic Platform publishes the recovery
// envelope on. Mirrors TopicWake from platform.go — kept distinct
// so the dashboard's recovery filter is one stream, not a regex
// over the unified topic.
const TopicRecovery = "recovery"

// RecoveryEvent is the contract pkg/events.Platform consumes for the
// recovery timeline. Mirrors WakeEvent's one-method interface — the
// concrete payload structs below are the only emitters.
type RecoveryEvent interface {
	Kind() string
	At() time.Time
	Subject() *string
	Payload() map[string]any
}

// addrString is shared with wake.go (same package) so the helper
// defined there is reusable; the typed structs below call it
// directly without redeclaration.

// NodeDrainingEvent — apid drain handler entry point.
type NodeDrainingEvent struct {
	EmitAt          time.Time
	NodeID          string
	NodeName        string
	InitiatedAt     time.Time
	OperatorSubject *string
}

func (e NodeDrainingEvent) Kind() string  { return NodeDraining }
func (e NodeDrainingEvent) At() time.Time { return e.EmitAt }
func (e NodeDrainingEvent) Subject() *string {
	if e.OperatorSubject == nil {
		return nil
	}
	return e.OperatorSubject
}
func (e NodeDrainingEvent) Payload() map[string]any {
	return map[string]any{
		"node_id":      e.NodeID,
		"node_name":    e.NodeName,
		"initiated_at": e.InitiatedAt,
	}
}

// NodeDrainedEvent — drain completion.
type NodeDrainedEvent struct {
	EmitAt               time.Time
	NodeID               string
	NodeName             string
	InitiatedAt          time.Time
	CompletedAt          time.Time
	DrainedInstanceCount int
}

func (e NodeDrainedEvent) Kind() string     { return NodeDrained }
func (e NodeDrainedEvent) At() time.Time    { return e.EmitAt }
func (e NodeDrainedEvent) Subject() *string { return nil }
func (e NodeDrainedEvent) Payload() map[string]any {
	return map[string]any{
		"node_id":                e.NodeID,
		"node_name":              e.NodeName,
		"initiated_at":           e.InitiatedAt,
		"completed_at":           e.CompletedAt,
		"drained_instance_count": e.DrainedInstanceCount,
	}
}

// NodeFailedEvent — heartbeat staleness → unavailable.
type NodeFailedEvent struct {
	EmitAt          time.Time
	NodeID          string
	NodeName        string
	LastHeartbeatAt time.Time
}

func (e NodeFailedEvent) Kind() string     { return NodeFailed }
func (e NodeFailedEvent) At() time.Time    { return e.EmitAt }
func (e NodeFailedEvent) Subject() *string { return nil }
func (e NodeFailedEvent) Payload() map[string]any {
	return map[string]any{
		"node_id":           e.NodeID,
		"node_name":         e.NodeName,
		"last_heartbeat_at": e.LastHeartbeatAt,
	}
}

// NodeRecoveredEvent — recovery sweep succeeded.
type NodeRecoveredEvent struct {
	EmitAt              time.Time
	NodeID              string
	NodeName            string
	RecoveryInitiatedAt time.Time
	MigratedCount       int
	RecreatedCount      int
}

func (e NodeRecoveredEvent) Kind() string     { return NodeRecovered }
func (e NodeRecoveredEvent) At() time.Time    { return e.EmitAt }
func (e NodeRecoveredEvent) Subject() *string { return nil }
func (e NodeRecoveredEvent) Payload() map[string]any {
	return map[string]any{
		"node_id":               e.NodeID,
		"node_name":             e.NodeName,
		"recovery_initiated_at": e.RecoveryInitiatedAt,
		"migrated_count":        e.MigratedCount,
		"recreated_count":       e.RecreatedCount,
	}
}

// InstanceMigratedEvent — live-migration Phase-4 ack.
type InstanceMigratedEvent struct {
	EmitAt       time.Time
	InstanceID   string
	AppID        string
	DeploymentID string
	SourceNodeID string
	DestNodeID   string
	LeaseID      string
}

func (e InstanceMigratedEvent) Kind() string     { return InstanceMigrated }
func (e InstanceMigratedEvent) At() time.Time    { return e.EmitAt }
func (e InstanceMigratedEvent) Subject() *string { return nil }
func (e InstanceMigratedEvent) Payload() map[string]any {
	return map[string]any{
		"instance_id":    e.InstanceID,
		"app_id":         e.AppID,
		"deployment_id":  e.DeploymentID,
		"source_node_id": e.SourceNodeID,
		"dest_node_id":   e.DestNodeID,
		"lease_id":       e.LeaseID,
	}
}

// InstanceRecreatedEvent — Engine.RecreateInstance completion.
type InstanceRecreatedEvent struct {
	EmitAt       time.Time
	InstanceID   string
	AppID        string
	DeploymentID string
	NodeID       string
	Reason       string
}

func (e InstanceRecreatedEvent) Kind() string     { return InstanceRecreated }
func (e InstanceRecreatedEvent) At() time.Time    { return e.EmitAt }
func (e InstanceRecreatedEvent) Subject() *string { return nil }
func (e InstanceRecreatedEvent) Payload() map[string]any {
	return map[string]any{
		"instance_id":   e.InstanceID,
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"node_id":       e.NodeID,
		"reason":        e.Reason,
	}
}

// InstanceFailedEvent — terminal failure after the recovery sweep.
type InstanceFailedEvent struct {
	EmitAt       time.Time
	InstanceID   string
	AppID        string
	DeploymentID string
	NodeID       string
	Reason       string
}

func (e InstanceFailedEvent) Kind() string     { return InstanceFailed }
func (e InstanceFailedEvent) At() time.Time    { return e.EmitAt }
func (e InstanceFailedEvent) Subject() *string { return nil }
func (e InstanceFailedEvent) Payload() map[string]any {
	return map[string]any{
		"instance_id":   e.InstanceID,
		"app_id":        e.AppID,
		"deployment_id": e.DeploymentID,
		"node_id":       e.NodeID,
		"reason":        e.Reason,
	}
}
