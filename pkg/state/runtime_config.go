package state

import (
	"encoding/json"
	"errors"
	"time"
)

// RuntimeConfigScope identifies the blast-radius boundary of a runtime
// configuration value. Global values are the fleet default; daemon and node
// overrides can narrow the blast radius without changing the setting's value
// shape. Control-plane overrides target the apid singleton.
type RuntimeConfigScope string

const (
	RuntimeConfigScopeGlobal       RuntimeConfigScope = "global"
	RuntimeConfigScopeControlPlane RuntimeConfigScope = "control_plane"
	RuntimeConfigScopeDaemon       RuntimeConfigScope = "daemon"
	RuntimeConfigScopeNode         RuntimeConfigScope = "node"
)

// RuntimeConfigApplyMode tells the operator console how a value is applied.
// Hot values are swapped into the process snapshot. The other modes are
// durable desired state and require a graceful/rolling controller or a
// break-glass deployment workflow.
type RuntimeConfigApplyMode string

const (
	RuntimeConfigApplyHot        RuntimeConfigApplyMode = "hot"
	RuntimeConfigApplyGraceful   RuntimeConfigApplyMode = "graceful"
	RuntimeConfigApplyRolling    RuntimeConfigApplyMode = "rolling"
	RuntimeConfigApplyBreakGlass RuntimeConfigApplyMode = "break_glass"
)

type RuntimeConfigStatus string

const (
	RuntimeConfigPending RuntimeConfigStatus = "pending"
	RuntimeConfigApplied RuntimeConfigStatus = "applied"
	RuntimeConfigFailed  RuntimeConfigStatus = "failed"
	RuntimeConfigBlocked RuntimeConfigStatus = "blocked"
)

// RuntimeConfigAckStatus records the result observed by one daemon/node for a
// specific desired configuration version. It is deliberately separate from
// RuntimeConfigStatus, whose row is the control-plane aggregate state.
type RuntimeConfigAckStatus string

const (
	RuntimeConfigAckApplied RuntimeConfigAckStatus = "applied"
	RuntimeConfigAckFailed  RuntimeConfigAckStatus = "failed"
)

// RuntimeConfigAck is the per-consumer convergence record written by daemon
// watchers. A missing row means that consumer has not observed the version.
type RuntimeConfigAck struct {
	Key            string
	Scope          RuntimeConfigScope
	ScopeID        string
	Consumer       string
	NodeID         string
	Version        int64
	Status         RuntimeConfigAckStatus
	EffectiveValue json.RawMessage
	Error          string
	UpdatedAt      time.Time
	AppliedAt      *time.Time
}

// RuntimeConfigOperationStatus is the lifecycle of an asynchronous operator
// configuration apply request. A row is never silently discarded: pending
// means it is waiting for a daemon/deployment controller, running means a
// controller has claimed it, and blocked/failed preserve the operator-facing
// reason the desired state is not effective.
type RuntimeConfigOperationStatus string

const (
	RuntimeConfigOperationPending   RuntimeConfigOperationStatus = "pending"
	RuntimeConfigOperationRunning   RuntimeConfigOperationStatus = "running"
	RuntimeConfigOperationSucceeded RuntimeConfigOperationStatus = "succeeded"
	RuntimeConfigOperationFailed    RuntimeConfigOperationStatus = "failed"
	RuntimeConfigOperationBlocked   RuntimeConfigOperationStatus = "blocked"
	RuntimeConfigOperationCancelled RuntimeConfigOperationStatus = "cancelled"
)

var (
	ErrRuntimeConfigNotFound = errors.New("state: runtime config not found")
	ErrRuntimeConfigConflict = errors.New("state: runtime config version conflict")
)

// RuntimeConfig is the desired/effective state of one operator setting.
// Values are JSON so the catalog can support booleans, bounded integers,
// durations, and enums without adding a schema migration for every setting.
type RuntimeConfig struct {
	ID             string
	Key            string
	Scope          RuntimeConfigScope
	ScopeID        string
	DesiredValue   json.RawMessage
	EffectiveValue json.RawMessage
	Version        int64
	// RolloutPercent is the deterministic percentage of matching daemon
	// identities that should receive this value. A value of 100 targets every
	// matching consumer; lower values are useful for canarying a daemon-scoped
	// or global flag while the lower-precedence value remains available.
	RolloutPercent int
	ApplyMode      RuntimeConfigApplyMode
	Status         RuntimeConfigStatus
	LastError      string
	ActorID        string
	Reason         string
	UpdatedAt      time.Time
	AppliedAt      *time.Time
}

// RuntimeConfigUpdate is the write-side request passed to the state layer.
// ExpectedVersion is optimistic concurrency: nil means last-write-wins, while
// a pointer requires that exact version (zero means the row must not exist).
type RuntimeConfigUpdate struct {
	Key          string
	Scope        RuntimeConfigScope
	ScopeID      string
	DesiredValue json.RawMessage
	// RolloutPercent is nil for the default (100%). A pointer preserves the
	// meaningful zero value, which disables this override for every target and
	// allows the watcher to fall back to a lower-precedence setting.
	RolloutPercent  *int
	ApplyMode       RuntimeConfigApplyMode
	ActorID         string
	Reason          string
	ExpectedVersion *int64
}

// RuntimeConfigOperation records one graceful, rolling, or break-glass apply
// request. Desired state lives in RuntimeConfig; this row is the durable
// workflow/audit handle the operator console polls.
type RuntimeConfigOperation struct {
	ID             string
	Key            string
	Scope          RuntimeConfigScope
	ScopeID        string
	Version        int64
	DesiredValue   json.RawMessage
	EffectiveValue json.RawMessage
	ApplyMode      RuntimeConfigApplyMode
	Status         RuntimeConfigOperationStatus
	Phase          string
	Error          string
	ActorID        string
	Reason         string
	TargetCount    int
	AppliedCount   int
	FailedCount    int
	RequestedAt    time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

// RuntimeConfigRevision is the append-only change history for one setting.
// It is separate from audit_log so the configuration screen can render the
// exact old/new JSON values and optimistic version without joining opaque
// audit payloads.
type RuntimeConfigRevision struct {
	ID             int64
	Key            string
	Scope          RuntimeConfigScope
	ScopeID        string
	Version        int64
	RolloutPercent int
	OldValue       json.RawMessage
	NewValue       json.RawMessage
	ActorID        string
	Reason         string
	CreatedAt      time.Time
}
