package faas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// EventOutboxSchema is opt-in application-owned PostgreSQL DDL. Payloads require
// restricted access. Helpers never install the schema or prune retained identities.
//
//go:embed event_outbox_schema.sql
var EventOutboxSchema string

var (
	ErrEventOutboxCommitUnknown = errors.New("event outbox transaction commit outcome is unknown")
	ErrEventOutboxStore         = errors.New("event outbox storage operation failed")
	ErrEventOutboxConflict      = errors.New("event outbox immutable publication conflicts")
	ErrEventOutboxEmpty         = errors.New("no event outbox publication is due")
	ErrEventOutboxClaimLost     = errors.New("event outbox recovery lease expired or replaced")
	ErrEventOutboxUnconfirmed   = errors.New("event outbox publication is unconfirmed")
	ErrEventOutboxBlocked       = errors.New("event outbox publication requires operator review")
)

// EventOutboxStatus excludes event data, request digest and claim capability.
type EventOutboxStatus struct {
	AppSlug        string     `json:"app_slug"`
	ProducerKey    string     `json:"producer_key"`
	State          string     `json:"state"`
	Attempts       int64      `json:"attempts"`
	NextAttemptAt  time.Time  `json:"next_attempt_at"`
	RetryUntil     time.Time  `json:"retry_until"`
	ClaimExpiresAt *time.Time `json:"claim_expires_at,omitempty"`
	AcceptedAt     *time.Time `json:"accepted_at,omitempty"`
	LastCode       string     `json:"last_code,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

const eventOutboxStatusColumns = `producer_key,app_slug,state,attempts,next_attempt_at,retry_until,claim_expires_at,accepted_at,last_code,created_at,updated_at`

func eventOutboxStatus(row eventIntentScanner) (EventOutboxStatus, error) {
	var s EventOutboxStatus
	var claim, accepted sql.NullTime
	err := row.Scan(&s.ProducerKey, &s.AppSlug, &s.State, &s.Attempts, &s.NextAttemptAt, &s.RetryUntil, &claim, &accepted, &s.LastCode, &s.CreatedAt, &s.UpdatedAt)
	if claim.Valid {
		s.ClaimExpiresAt = &claim.Time
	}
	if accepted.Valid {
		s.AcceptedAt = &accepted.Time
	}
	return s, err
}
func eventOutboxStoreError(ctx context.Context) error {
	// Driver errors can contain rejected payloads. Never propagate them.
	return errors.Join(ErrEventOutboxStore, ctx.Err())
}

// SaveEventOutbox stages an immutable publication in the caller's transaction.
// Commit business writes and this row together; on any error roll back the whole
// transaction. The caller owns commit and uncertain-commit reconciliation. The
// stable key and original retry deadline are preserved by repeated saves.
// retryWindow defaults to seven days and accepts one second..seven days.
func SaveEventOutbox(ctx context.Context, tx *sql.Tx, accountID, appID, slug string, request AppPublishEventRequest, retryWindow time.Duration) (EventOutboxStatus, error) {
	var zero EventOutboxStatus
	if ctx == nil || tx == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	if retryWindow == 0 {
		retryWindow = 7 * 24 * time.Hour
	}
	if retryWindow < time.Second || retryWindow > 7*24*time.Hour || retryWindow%time.Millisecond != 0 || !eventOutboxSlug(slug) || len(request.Data) > 1048576 || request.Validate() != nil || strings.TrimSpace(request.Type) == "" || len(request.Type) > 256 || len(request.SchemaVersion) > 64 || request.ExpectedAppID != "" && request.ExpectedAppID != app {
		return zero, ErrInvalidEventConsumerRequest
	}
	request.ExpectedAppID = app
	// Marshal once: the same occurrence time and bytes survive every retry.
	body, err := json.Marshal(request)
	if err != nil || len(body) > 1048576 {
		return zero, ErrInvalidEventConsumerRequest
	}
	digest := sha256.Sum256(body)
	_, err = tx.ExecContext(ctx, `WITH stamp AS MATERIALIZED (SELECT clock_timestamp() AS t)
 INSERT INTO public.gregale_event_outbox(account_id,app_id,producer_key,app_slug,request_digest,request_body,retry_until,created_at,updated_at,next_attempt_at)
 SELECT $1::uuid,$2::uuid,$3,$4,$5,$6,t+$7::bigint*interval '1 millisecond',t,t,t FROM stamp
 ON CONFLICT(account_id,app_id,producer_key) DO NOTHING`, account, app, request.Key, slug, digest[:], string(body), retryWindow.Milliseconds())
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	var storedDigest []byte
	var storedSlug string
	err = tx.QueryRowContext(ctx, `SELECT request_digest,app_slug FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 FOR UPDATE`, account, app, request.Key).Scan(&storedDigest, &storedSlug)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	if !bytes.Equal(storedDigest, digest[:]) || storedSlug != slug {
		return zero, ErrEventOutboxConflict
	}
	s, err := eventOutboxStatus(tx.QueryRowContext(ctx, `SELECT `+eventOutboxStatusColumns+` FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3`, account, app, request.Key))
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return s, nil
}

// GetEventOutboxStatus reads retained local state, not consumer delivery success.
func GetEventOutboxStatus(ctx context.Context, db *sql.DB, accountID, appID, key string) (EventOutboxStatus, error) {
	var zero EventOutboxStatus
	if ctx == nil || db == nil || !eventOutboxKey(key) {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	s, err := eventOutboxStatus(db.QueryRowContext(ctx, `SELECT `+eventOutboxStatusColumns+` FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3`, account, app, key))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrNotFound
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return s, nil
}
