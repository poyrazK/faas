package faas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// EventOutboxReconciliationOptions optionally supplies a renamed app's current
// slug for read-only verification. Options never change saved event content.
type EventOutboxReconciliationOptions struct {
	// CommandID enables durable history and result replay; install updated DDL first.
	CommandID      string
	AppSlug        string
	RequestTimeout time.Duration
}

// EventOutboxReconciliationResult reports retained evidence separately from local
// publication state. Unavailable evidence never confirms or resumes work.
type EventOutboxReconciliationResult struct {
	RetryPolicy *EventOutboxStoredRetryPolicy `json:"retry_policy,omitempty"`
	Outcome     string                        `json:"outcome"`
	Publication EventOutboxStatus             `json:"publication"`
	ObservedAt  *time.Time                    `json:"observed_at,omitempty"`
}

var ErrEventOutboxBusy = errors.New("event outbox publication has a live recovery claim")

func eventOutboxSlug(slug string) bool {
	return len(slug) > 0 && len(slug) <= 256 && strings.TrimSpace(slug) == slug && !strings.ContainsAny(slug, "/\\?#\x00")
}
func eventOutboxKey(key string) bool {
	return (AppPublishEventRequest{Key: key, Data: json.RawMessage(`null`)}).Validate() == nil
}

// ReconcileEventOutbox verifies the saved content through the read-only endpoint.
// Matching retained acceptance within the original intent window can settle the
// row even after its deadline. It never publishes, resumes, or extends a deadline.
// Live claims are rejected; settlement compares the original metadata and digest
// so concurrent recovery/control changes invalidate the verification snapshot.
func ReconcileEventOutbox(ctx context.Context, db *sql.DB, client *Client, accountID, appID, key string, options EventOutboxReconciliationOptions) (EventOutboxReconciliationResult, error) {
	if ctx == nil || db == nil || client == nil || client.Client == nil {
		return EventOutboxReconciliationResult{}, ErrInvalidEventConsumerRequest
	}
	if options.CommandID != "" {
		return reconcileEventOutboxControl(ctx, db, client, accountID, appID, key, options)
	}
	return reconcileEventOutbox(ctx, db, client, accountID, appID, key, options)
}

type eventOutboxControlStore interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func reconcileEventOutbox(ctx context.Context, db eventOutboxControlStore, client *Client, accountID, appID, key string, options EventOutboxReconciliationOptions) (EventOutboxReconciliationResult, error) {
	var out EventOutboxReconciliationResult
	if ctx == nil || db == nil || client == nil || client.Client == nil || !eventOutboxKey(key) {
		return out, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return out, err
	}
	if options.RequestTimeout == 0 {
		options.RequestTimeout = 10 * time.Second
	}
	if options.RequestTimeout < time.Millisecond || options.RequestTimeout > 30*time.Second || options.AppSlug != "" && !eventOutboxSlug(options.AppSlug) {
		return out, ErrInvalidEventConsumerRequest
	}
	// Load metadata and request from the same snapshot, without a lock across HTTP.
	var body sql.NullString
	var digest []byte
	var live bool
	var s EventOutboxStatus
	var expiry, accepted sql.NullTime
	err = db.QueryRowContext(ctx, `SELECT `+eventOutboxStatusColumns+`,request_body,request_digest,coalesce(claim_expires_at>clock_timestamp(),false) FROM public.gregale_event_outbox WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3`, account, app, key).Scan(&s.ProducerKey, &s.AppSlug, &s.State, &s.Attempts, &s.NextAttemptAt, &s.RetryUntil, &expiry, &accepted, &s.LastCode, &s.CreatedAt, &s.UpdatedAt, &body, &digest, &live)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	if expiry.Valid {
		s.ClaimExpiresAt = &expiry.Time
	}
	if accepted.Valid {
		s.AcceptedAt = &accepted.Time
	}
	out.Publication = s
	out.Outcome = "unverified"
	if s.State == "published" {
		out.Outcome = "already_published"
		return out, nil
	}
	if live {
		return out, ErrEventOutboxBusy
	}
	hash := sha256.Sum256([]byte(body.String))
	var request AppPublishEventRequest
	if !body.Valid || !bytes.Equal(hash[:], digest) || json.Unmarshal([]byte(body.String), &request) != nil || request.Validate() != nil || request.Key != key || request.ExpectedAppID != app {
		return out, ErrEventOutboxConflict
	}
	slug := s.AppSlug
	if options.AppSlug != "" {
		slug = options.AppSlug
	}
	callCtx, cancel := context.WithTimeout(ctx, options.RequestTimeout)
	evidence, verifyErr := client.VerifyAppEventPublication(callCtx, slug, request)
	cancel()
	out.Outcome = "unverified"
	if verifyErr != nil {
		return out, errors.Join(ErrEventOutboxUnconfirmed, ctx.Err())
	}
	id := sha256.Sum256([]byte(key))
	eventID := "key." + hex.EncodeToString(id[:])
	if evidence.AppID != app || evidence.Source != "app."+app || evidence.EventID != eventID || evidence.ObservedAt.IsZero() {
		return out, ErrEventOutboxUnconfirmed
	}
	out.ObservedAt = &evidence.ObservedAt
	if evidence.Status == "unavailable" {
		out.Outcome = "unavailable"
		return out, nil
	}
	if evidence.Status == "conflict" {
		out.Outcome = "conflict"
		return out, ErrEventOutboxConflict
	}
	receipt := evidence.Receipt
	if evidence.Status != "match" || receipt == nil || receipt.AccountID != account || receipt.ID != eventID || receipt.AcceptedAt.IsZero() || receipt.AcceptedAt.Before(s.CreatedAt) || receipt.AcceptedAt.After(s.RetryUntil) || receipt.AcceptedAt.After(evidence.ObservedAt) {
		return out, ErrEventOutboxUnconfirmed
	}
	// No live/expired claim may have appeared or been replaced while reading HTTP.
	settled, err := eventOutboxStatus(db.QueryRowContext(ctx, `UPDATE public.gregale_event_outbox SET state='published',accepted_at=$9::timestamptz,request_body=NULL,claim_token=NULL,claim_expires_at=NULL,last_code='publication_confirmed',updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 AND state=$4 AND updated_at=$5::timestamptz AND attempts=$6::bigint AND request_digest=$7 AND app_slug=$8 AND (claim_expires_at IS NOT DISTINCT FROM $10::timestamptz) AND (claim_expires_at IS NULL OR claim_expires_at<=clock_timestamp()) RETURNING `+eventOutboxStatusColumns, account, app, key, s.State, s.UpdatedAt, s.Attempts, digest, s.AppSlug, receipt.AcceptedAt, s.ClaimExpiresAt))
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrEventOutboxConflict
	}
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	out.Publication = settled
	out.Outcome = "published"
	return out, nil
}

// EventOutboxResumeOptions requires a status snapshot read by the operator.
// AppSlug may correct an app rename; the immutable request retains its UUID guard.
type EventOutboxResumeOptions struct {
	// CommandID enables durable history and result replay; install updated DDL first.
	CommandID         string
	ExpectedUpdatedAt time.Time
	ExpectedAttempts  int64
	AppSlug           string
}

// ResumeEventOutbox explicitly re-enables blocked work within its original window.
// It sends no HTTP and performs no automatic inference from unavailable evidence.
// Stale snapshots, terminal confirmation, live claims and expiry cannot resume.
func ResumeEventOutbox(ctx context.Context, db *sql.DB, accountID, appID, key string, options EventOutboxResumeOptions) (EventOutboxStatus, error) {
	if ctx == nil || db == nil {
		return EventOutboxStatus{}, ErrInvalidEventConsumerRequest
	}
	if options.CommandID != "" {
		return resumeEventOutboxControl(ctx, db, accountID, appID, key, options)
	}
	return resumeEventOutbox(ctx, db, accountID, appID, key, options)
}

func resumeEventOutbox(ctx context.Context, db eventOutboxControlStore, accountID, appID, key string, options EventOutboxResumeOptions) (EventOutboxStatus, error) {
	var zero EventOutboxStatus
	if ctx == nil || db == nil || !eventOutboxKey(key) || options.ExpectedUpdatedAt.IsZero() || options.ExpectedAttempts < 0 || options.AppSlug != "" && !eventOutboxSlug(options.AppSlug) {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	status, err := eventOutboxStatus(db.QueryRowContext(ctx, `UPDATE public.gregale_event_outbox SET state='unconfirmed',app_slug=CASE WHEN $6='' THEN app_slug ELSE $6 END,last_code='unconfirmed',next_attempt_at=clock_timestamp(),updated_at=clock_timestamp() WHERE account_id=$1::uuid AND app_id=$2::uuid AND producer_key=$3 AND state='blocked' AND updated_at=$4::timestamptz AND attempts=$5::bigint AND claim_token IS NULL AND retry_until>clock_timestamp() RETURNING `+eventOutboxStatusColumns, account, app, key, options.ExpectedUpdatedAt, options.ExpectedAttempts, options.AppSlug))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrEventOutboxConflict
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return status, nil
}
