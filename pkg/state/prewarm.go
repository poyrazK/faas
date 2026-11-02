package state

import (
	"context"
	"errors"
	"strings"
	"time"
)

// PrewarmIntent is a durable request to restore capacity before a known
// demand window.  The scheduler treats WakeAt as the start of the demand
// window and claims the row during the configured lead time.
type PrewarmIntent struct {
	ID            string
	AppID         string
	AccountID     string
	Count         int
	WakeAt        time.Time
	ExpiresAt     time.Time
	Trigger       string
	Status        string
	CreatedAt     time.Time
	ClaimedAt     *time.Time
	FiredAt       *time.Time
	AdmittedCount int
	Outcome       string
	LastError     string
}

const (
	PrewarmStatusPending   = "pending"
	PrewarmStatusRunning   = "running"
	PrewarmStatusSucceeded = "succeeded"
	PrewarmStatusFailed    = "failed"
	PrewarmStatusCancelled = "cancelled"

	PrewarmTriggerCalendar = "calendar"
	PrewarmTriggerCron     = "cron"
	PrewarmTriggerPattern  = "pattern"
	PrewarmTriggerWebhook  = "webhook"
)

// PrewarmStore is intentionally an optional Store capability.  Keeping it
// separate lets older test stores and integrations continue to satisfy the
// large Store interface while PgStore/MemStore gain the durable scheduler
// primitive.
type PrewarmStore interface {
	CreatePrewarmIntent(ctx context.Context, appID, accountID string, count int, wakeAt, expiresAt time.Time, trigger string) (PrewarmIntent, error)
	PrewarmIntentByID(ctx context.Context, id string) (PrewarmIntent, error)
	ListPrewarmIntentsForApp(ctx context.Context, appID string, limit int) ([]PrewarmIntent, error)
	ListDuePrewarmIntents(ctx context.Context, before, now time.Time, limit int) ([]PrewarmIntent, error)
	ActivePrewarmFloor(ctx context.Context, appID string, now time.Time) (int, error)
	ClaimPrewarmIntent(ctx context.Context, id string, claimedAt time.Time) (PrewarmIntent, bool, error)
	CompletePrewarmIntent(ctx context.Context, id string, firedAt time.Time, admittedCount int, outcome string) error
	FailPrewarmIntent(ctx context.Context, id string, firedAt time.Time, cause string) error
	CancelPrewarmIntent(ctx context.Context, id, accountID string) error
}

// PrewarmFloorsReader reads the active prewarm floor of many apps in one query
// (ADR 20261102160622): the reaper reads every owned app's floor each tick,
// and one ActivePrewarmFloor per app grew its tick with the app count. An app
// without an active intent is absent from the map (floor 0). Callers fall
// back to ActivePrewarmFloor when the store does not implement it.
type PrewarmFloorsReader interface {
	ActivePrewarmFloors(ctx context.Context, appIDs []string, now time.Time) (map[string]int, error)
}

// PrewarmExpiryStore is an optional scheduler capability. Expired pending
// intents are terminalized as failed rows with outcome "expired" so API
// clients do not see a stale pending intent forever. It is separate from
// PrewarmStore to preserve compatibility with older integrations and fakes.
type PrewarmExpiryStore interface {
	ListExpiredPrewarmIntents(ctx context.Context, now time.Time, limit int) ([]PrewarmIntent, error)
	ExpirePrewarmIntent(ctx context.Context, id string, expiredAt time.Time) (bool, error)
}

// ValidatePrewarmIntent contains the store-independent invariants shared by
// the API and both persistence implementations.
func ValidatePrewarmIntent(count int, wakeAt, expiresAt, now time.Time, trigger string) error {
	if count <= 0 {
		return errors.New("prewarm count must be positive")
	}
	if wakeAt.IsZero() || !wakeAt.After(now) {
		return errors.New("prewarm wake_at must be in the future")
	}
	if expiresAt.IsZero() || !expiresAt.After(wakeAt) {
		return errors.New("prewarm expires_at must be after wake_at")
	}
	if strings.TrimSpace(trigger) == "" {
		return errors.New("prewarm trigger is required")
	}
	return nil
}
