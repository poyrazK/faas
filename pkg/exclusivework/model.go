// Package exclusivework defines managed-operation policy and ownership values.
// Authority is persisted outside the guest and checked by the state store.
package exclusivework

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var (
	ErrBusy             = errors.New("exclusive operation busy")
	ErrStaleOwner       = errors.New("exclusive operation ownership lost")
	ErrIdentityConflict = errors.New("exclusive operation identity conflict")
	NamePattern         = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	EffectTypePattern   = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
)

type Policy struct {
	Name              string   `json:"name"`
	Scope             string   `json:"scope"`
	EnvironmentID     string   `json:"environment_id,omitempty"`
	MemberAppIDs      []string `json:"member_app_ids,omitempty"`
	MemberJobIDs      []string `json:"member_job_ids,omitempty"`
	Contention        string   `json:"contention"`
	LeaseSeconds      int      `json:"lease_seconds"`
	MaxAttemptSeconds int      `json:"max_attempt_seconds"`
	MaxAttempts       int      `json:"max_attempts,omitempty"`
	RetryAfterSeconds int      `json:"retry_after_seconds,omitempty"`
}

// Claim is an internal capability. Renewal secrets must not appear in receipt
// responses, logs, or metrics. Incarnation is assigned by the runtime host.
type Claim struct {
	OperationID   string    `json:"operation_id"`
	AccountID     string    `json:"account_id"`
	Generation    int64     `json:"generation"`
	Token         string    `json:"token"`
	IncarnationID string    `json:"incarnation_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	Deadline      time.Time `json:"deadline"`
}

// Effect is a platform-controlled outbox insertion committed with the result.
// External delivery remains at least once and needs a compatible adapter.
type Effect struct {
	Name      string          `json:"name"`
	Payload   json.RawMessage `json:"payload"`
	WebhookID string          `json:"webhook_id,omitempty"`
	Type      string          `json:"type,omitempty"`
}
