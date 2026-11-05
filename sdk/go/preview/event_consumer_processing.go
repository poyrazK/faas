package preview

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EventConsumerProcessingSchema is opt-in application-owned PostgreSQL DDL.
// Install alongside EventConsumerReceiptSchema; helpers never install or prune it.
//
//go:embed event_consumer_processing_schema.sql
var EventConsumerProcessingSchema string

var (
	ErrEventConsumerProcessingBusy      = errors.New("event consumer processing lease is held")
	ErrEventConsumerProcessingLeaseLost = errors.New("event consumer processing lease expired or replaced")
)

// EventConsumerProcessingLease is an opaque capability bound to one immutable
// request. Never log its token. Copying it does not create a new claim.
type EventConsumerProcessingLease struct {
	completed bool
	input     EventConsumerRequest
	token     [32]byte
	valid     bool
}

// ClaimEventConsumerProcessing acquires a database-clock lease for preparation.
// Duration must be positive whole milliseconds. Expired claims can be replaced;
// their input digest stays immutable. Unknown commit outcomes require waiting
// for expiry, since an error does not prove the claim was never committed.
func ClaimEventConsumerProcessing(ctx context.Context, db *sql.DB, input EventConsumerRequest, duration time.Duration) (EventConsumerProcessingLease, error) {
	var out EventConsumerProcessingLease
	if !input.valid || db == nil || !validProcessingDuration(duration) {
		return out, ErrInvalidEventConsumerRequest
	}
	out.input = input
	if _, err := rand.Read(out.token[:]); err != nil {
		return EventConsumerProcessingLease{}, fmt.Errorf("processing token: %w", err)
	}
	identity, digest := eventConsumerDigests(input)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return EventConsumerProcessingLease{}, fmt.Errorf("processing claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "gregale.event-consumer-inbox.v1:"+hex.EncodeToString(identity[:])); err != nil {
		return EventConsumerProcessingLease{}, fmt.Errorf("processing claim lock: %w", err)
	}
	// Completed receipts need no new processing row. Complete validates and
	// replays the receipt even after its former lease has been removed.
	var matching bool
	err = tx.QueryRowContext(ctx, `SELECT account_id=$2::uuid AND app_id=$3::uuid AND subscription_id=$4::uuid AND acceptance_id=$5 AND event_source=$6 AND event_id=$7 AND request_digest=$8 FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1`, identity[:], input.identity.AccountID, input.identity.AppID, input.identity.SubscriptionID, input.identity.AcceptanceID, input.identity.Source, input.identity.EventID, digest[:]).Scan(&matching)
	if err == nil {
		if !matching {
			return EventConsumerProcessingLease{}, ErrEventConsumerReceiptConflict
		}
		if err := tx.Commit(); err != nil {
			return EventConsumerProcessingLease{}, fmt.Errorf("%w: %w", ErrEventConsumerCommitUnknown, err)
		}
		out.valid = true
		out.completed = true
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return EventConsumerProcessingLease{}, fmt.Errorf("processing receipt lookup: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.gregale_event_consumer_processing (identity_digest,request_digest,claim_token,expires_at) VALUES ($1,$2,$3,clock_timestamp()+$4::bigint*interval '1 millisecond') ON CONFLICT (identity_digest) DO NOTHING`, identity[:], digest[:], out.token[:], duration.Milliseconds()); err != nil {
		return EventConsumerProcessingLease{}, fmt.Errorf("processing claim insert: %w", err)
	}
	var stored, token []byte
	var expired bool
	if err := tx.QueryRowContext(ctx, `SELECT request_digest,claim_token,expires_at<=clock_timestamp() FROM public.gregale_event_consumer_processing WHERE identity_digest=$1 FOR UPDATE`, identity[:]).Scan(&stored, &token, &expired); err != nil {
		return EventConsumerProcessingLease{}, fmt.Errorf("processing claim read: %w", err)
	}
	if !bytes.Equal(stored, digest[:]) {
		return EventConsumerProcessingLease{}, ErrEventConsumerReceiptConflict
	}
	if !bytes.Equal(token, out.token[:]) {
		if !expired {
			return EventConsumerProcessingLease{}, ErrEventConsumerProcessingBusy
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.gregale_event_consumer_processing SET claim_token=$2,expires_at=clock_timestamp()+$3::bigint*interval '1 millisecond' WHERE identity_digest=$1`, identity[:], out.token[:], duration.Milliseconds()); err != nil {
			return EventConsumerProcessingLease{}, fmt.Errorf("processing claim takeover: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return EventConsumerProcessingLease{}, fmt.Errorf("%w: %w", ErrEventConsumerCommitUnknown, err)
	}
	out.valid = true
	return out, nil
}

// Renew extends an unexpired claim from the database clock. It never revives an
// expired claim. Stop preparation if renewal fails.
func (lease EventConsumerProcessingLease) Renew(ctx context.Context, db *sql.DB, duration time.Duration) error {
	if !lease.valid || db == nil || !validProcessingDuration(duration) {
		return ErrInvalidEventConsumerRequest
	}
	identity, digest := eventConsumerDigests(lease.input)
	result, err := db.ExecContext(ctx, `UPDATE public.gregale_event_consumer_processing SET expires_at=clock_timestamp()+$4::bigint*interval '1 millisecond' WHERE identity_digest=$1 AND request_digest=$2 AND claim_token=$3 AND expires_at>clock_timestamp()`, identity[:], digest[:], lease.token[:], duration.Milliseconds())
	return processingMutation(result, err)
}

// Complete fences the claim, runs business writes, saves the receipt and removes
// the claim atomically. Expiry during the callback rolls back business writes.
// Previously committed receipts replay without executing the callback.
func (lease EventConsumerProcessingLease) Complete(ctx context.Context, db *sql.DB, handler func(EventConsumerSQLTransaction) (json.RawMessage, error)) (EventConsumerTransactionResult, error) {
	if !lease.valid {
		return EventConsumerTransactionResult{}, ErrInvalidEventConsumerRequest
	}
	return withEventConsumerTransaction(ctx, db, lease.input, handler, &lease)
}

func (lease EventConsumerProcessingLease) check(ctx context.Context, tx *sql.Tx) error {
	identity, digest := eventConsumerDigests(lease.input)
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT request_digest=$2 AND claim_token=$3 AND expires_at>clock_timestamp() FROM public.gregale_event_consumer_processing WHERE identity_digest=$1 FOR UPDATE`, identity[:], digest[:], lease.token[:]).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !valid) {
		return ErrEventConsumerProcessingLeaseLost
	}
	if err != nil {
		return fmt.Errorf("processing fence: %w", err)
	}
	return nil
}

func (lease EventConsumerProcessingLease) release(ctx context.Context, tx *sql.Tx) error {
	identity, digest := eventConsumerDigests(lease.input)
	result, err := tx.ExecContext(ctx, `DELETE FROM public.gregale_event_consumer_processing WHERE identity_digest=$1 AND request_digest=$2 AND claim_token=$3 AND expires_at>clock_timestamp()`, identity[:], digest[:], lease.token[:])
	return processingMutation(result, err)
}

func processingMutation(result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("processing lease mutation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("processing lease rows: %w", err)
	}
	if count != 1 {
		return ErrEventConsumerProcessingLeaseLost
	}
	return nil
}

func validProcessingDuration(duration time.Duration) bool {
	return duration > 0 && duration%time.Millisecond == 0
}

// Completed reports that Claim observed a committed receipt. Skip preparation
// and call Complete to retrieve the validated saved result. This observation
// does not authorize deleting receipts or acknowledging before Complete succeeds.
func (lease EventConsumerProcessingLease) Completed() bool { return lease.valid && lease.completed }

// String redacts the claim capability when formatting a lease for diagnostics.
func (lease EventConsumerProcessingLease) String() string {
	return "EventConsumerProcessingLease{redacted}"
}

// GoString also redacts the capability with Go-syntax formatting.
func (lease EventConsumerProcessingLease) GoString() string { return lease.String() }
