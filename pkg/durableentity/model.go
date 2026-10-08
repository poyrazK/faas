// Package durableentity implements SQL-free logical state using immutable
// snapshots and an ownership/state manifest committed by compare-and-swap.
// It is an internal prototype, not a customer execution runtime (ADR-712).
package durableentity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// LimitError preserves the budget and observed size without exposing state.
type LimitError struct {
	Budget   string
	Limit    int64
	Observed int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("durable entity %s budget: limit %d, observed %d", e.Budget, e.Limit, e.Observed)
}

func (*LimitError) Unwrap() error { return ErrLimit }

func exceeded(budget string, limit, observed int) error {
	return &LimitError{Budget: budget, Limit: int64(limit), Observed: int64(observed)}
}

var (
	ErrNotFound         = errors.New("durable entity object not found")
	ErrConflict         = errors.New("durable entity conditional write conflict")
	ErrBusy             = errors.New("durable entity already owned")
	ErrStaleOwner       = errors.New("durable entity ownership lost")
	ErrRequestConflict  = errors.New("durable entity request identity reused with different payload")
	ErrUncertain        = errors.New("durable entity write outcome uncertain; retry with the same request identity")
	ErrInvalid          = errors.New("invalid durable entity request")
	ErrCorrupt          = errors.New("durable entity committed state is missing or corrupt")
	ErrLimit            = errors.New("durable entity prototype budget exceeded")
	ErrUnsupported      = errors.New("object store does not satisfy durable entity conditional-write requirements")
	ErrInventoryPending = errors.New("durable entity storage accounting is pending")
)

// ObjectStore versions are opaque CAS tokens. Get must return body and version
// from one strongly consistent read. Put uses conditional create when version
// is empty. ErrConflict is a definite rejection; other Put errors may have
// committed. Implementations MUST NOT automatically retry dispatched writes.
type ObjectStore interface {
	Get(ctx context.Context, key string, maxBytes int64) (body []byte, version string, err error)
	Put(ctx context.Context, key string, body []byte, expectedVersion string) (version string, err error)
}

type ID struct {
	AccountID string `json:"account_id"`
	AppID     string `json:"app_id"`
	// Optional only for the original trusted harness. Runtime ingress supplies
	// an immutable environment identity and a verified tenant identity.
	EnvironmentID string `json:"environment_id,omitempty"`
	TenantID      string `json:"tenant_id,omitempty"`
	Namespace     string `json:"namespace"`
	Key           string `json:"key"`
}

func validIdentity(s string) bool {
	if strings.TrimSpace(s) == "" || len(s) > api.MaxDurableEntityIdentityBytes || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func (id ID) valid() bool {
	return validIdentity(id.AccountID) && validIdentity(id.AppID) && validIdentity(id.Namespace) && validIdentity(id.Key) &&
		(id.EnvironmentID == "" || validIdentity(id.EnvironmentID)) && (id.TenantID == "" || validIdentity(id.TenantID))
}

func (id ID) prefix() string {
	b, _ := json.Marshal(id)
	return "gregale/durable-entities/v1/entities/" + digest(b) + "/"
}

func digest(b []byte) string {
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}

// Claim is a private platform capability. OwnerID identifies a process
// incarnation. Never give customer code the token or bucket credentials.
type Claim struct {
	ID        ID
	OwnerID   string
	Epoch     uint64
	Token     string `json:"-"`
	ExpiresAt time.Time
}

type View struct {
	Data    json.RawMessage `json:"data"`
	Version uint64          `json:"version"`
	AlarmAt *time.Time      `json:"alarm_at,omitempty"`
}

type Request struct {
	ID      string
	Payload json.RawMessage
}

// Transition replaces business state and alarm state together. Nil AlarmAt
// clears an alarm. Callbacks must be pure: no external side effects. A callback
// can run even when publication ultimately conflicts or fails.
type Transition struct {
	Data    json.RawMessage `json:"data"`
	Result  json.RawMessage `json:"result"`
	AlarmAt *time.Time      `json:"alarm_at,omitempty"`
}

type Result struct {
	Value    json.RawMessage `json:"value"`
	Version  uint64          `json:"version"`
	Replayed bool            `json:"replayed"`
}

type receipt struct {
	Fingerprint string          `json:"fingerprint"`
	Result      json.RawMessage `json:"result"`
	Version     uint64          `json:"version"`
}

type snapshot struct {
	Schema         int                `json:"schema"`
	ID             ID                 `json:"entity"`
	Version        uint64             `json:"version"`
	Data           json.RawMessage    `json:"data"`
	AlarmAt        *time.Time         `json:"alarm_at,omitempty"`
	Receipts       map[string]receipt `json:"receipts,omitempty"` // Schema 1 only.
	ReceiptRoot    *journalRef        `json:"receipt_root,omitempty"`
	LegacyReceipts *objectRef         `json:"legacy_receipts,omitempty"`
}

type objectRef struct {
	Key   string `json:"key"`
	Hash  string `json:"hash"`
	bytes int64  // Encoded size of a newly planned immutable object; never serialized.
}

type journalRef struct {
	objectRef
	Prefix string `json:"prefix"`
}

type manifest struct {
	Schema            int            `json:"schema"`
	ID                ID             `json:"entity"`
	OwnerID           string         `json:"owner_id"`
	Token             string         `json:"claim_token"`
	Epoch             uint64         `json:"epoch"`
	Revision          string         `json:"revision"`
	ExpiresAt         time.Time      `json:"expires_at"`
	SnapshotKey       string         `json:"snapshot_key"`
	SnapshotHash      string         `json:"snapshot_hash"`
	Version           uint64         `json:"state_version"`
	Generation        uint64         `json:"storage_generation,omitempty"`
	StorageLimitBytes int64          `json:"storage_limit_bytes,omitempty"`
	StorageUsage      *StorageUsage  `json:"storage_usage,omitempty"`
	AlarmDelivery     *alarmDelivery `json:"alarm_delivery,omitempty"`
}
