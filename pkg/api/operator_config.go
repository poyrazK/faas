package api

import "encoding/json"

// OperatorRuntimeConfig is the public wire shape for one entry in the
// operator runtime-configuration catalog. Values remain JSON because the
// catalog intentionally supports booleans, bounded integers, durations, and
// enums without changing the API for every new setting.
type OperatorRuntimeConfig struct {
	Key               string                     `json:"key"`
	Label             string                     `json:"label"`
	Description       string                     `json:"description"`
	Category          string                     `json:"category"`
	Kind              string                     `json:"kind"`
	DefaultValue      json.RawMessage            `json:"default_value"`
	DesiredValue      json.RawMessage            `json:"desired_value"`
	EffectiveValue    json.RawMessage            `json:"effective_value"`
	Scope             string                     `json:"scope"`
	ScopeID           string                     `json:"scope_id,omitempty"`
	RolloutPercent    int                        `json:"rollout_percent"`
	RolloutState      string                     `json:"rollout_state"`
	Source            string                     `json:"source"`
	ApplyMode         string                     `json:"apply_mode"`
	ControllerEnabled bool                       `json:"controller_enabled"`
	Mutable           bool                       `json:"mutable"`
	Sensitive         bool                       `json:"sensitive"`
	Status            string                     `json:"status"`
	LastError         string                     `json:"last_error,omitempty"`
	Version           int64                      `json:"version"`
	UpdatedAt         string                     `json:"updated_at,omitempty"`
	AppliedAt         string                     `json:"applied_at,omitempty"`
	Acks              []OperatorRuntimeConfigAck `json:"acks,omitempty"`
}

type OperatorRuntimeConfigAck struct {
	Consumer       string          `json:"consumer"`
	NodeID         string          `json:"node_id,omitempty"`
	Version        int64           `json:"version"`
	Status         string          `json:"status"`
	EffectiveValue json.RawMessage `json:"effective_value,omitempty"`
	Error          string          `json:"error,omitempty"`
	UpdatedAt      string          `json:"updated_at"`
	AppliedAt      string          `json:"applied_at,omitempty"`
}

// OperatorRuntimeConfigOperation is the polling shape for a graceful,
// rolling, or break-glass configuration apply request.
type OperatorRuntimeConfigOperation struct {
	ID             string          `json:"id"`
	Key            string          `json:"key"`
	Scope          string          `json:"scope"`
	ScopeID        string          `json:"scope_id"`
	Version        int64           `json:"version"`
	DesiredValue   json.RawMessage `json:"desired_value"`
	EffectiveValue json.RawMessage `json:"effective_value"`
	ApplyMode      string          `json:"apply_mode"`
	Status         string          `json:"status"`
	Phase          string          `json:"phase"`
	Error          string          `json:"error,omitempty"`
	Reason         string          `json:"reason"`
	TargetCount    int             `json:"target_count"`
	AppliedCount   int             `json:"applied_count"`
	FailedCount    int             `json:"failed_count"`
	RequestedAt    string          `json:"requested_at"`
	StartedAt      string          `json:"started_at,omitempty"`
	FinishedAt     string          `json:"finished_at,omitempty"`
}

type OperatorRuntimeConfigRevision struct {
	ID             int64           `json:"id"`
	Key            string          `json:"key"`
	Scope          string          `json:"scope"`
	ScopeID        string          `json:"scope_id"`
	Version        int64           `json:"version"`
	RolloutPercent int             `json:"rollout_percent"`
	OldValue       json.RawMessage `json:"old_value"`
	NewValue       json.RawMessage `json:"new_value"`
	ActorID        string          `json:"actor_id,omitempty"`
	Reason         string          `json:"reason"`
	CreatedAt      string          `json:"created_at"`
}

// RollbackOperatorRuntimeConfigRequest is the body for
// POST /v1/admin/config/{key}/rollback. ExpectedVersion is optional;
// when present, the server rejects the request if the live revision has
// changed since the operator loaded the history.
type RollbackOperatorRuntimeConfigRequest struct {
	Version         int64  `json:"version"`
	Reason          string `json:"reason"`
	ExpectedVersion *int64 `json:"expected_version,omitempty"`
}
