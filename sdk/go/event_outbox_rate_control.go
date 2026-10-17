package faas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// EventOutboxRateLimitOptions configures one token per Interval, up to Burst.
// Zero interval/burst use one second/one token. CommandID is a nonzero UUID.
type EventOutboxRateLimitOptions struct {
	CommandID        string
	ExpectedRevision int64
	Enabled          bool
	Interval         time.Duration
	Burst            int64
}
type EventOutboxRateLimitCommandReceipt struct {
	CommandID  string                     `json:"command_id"`
	Sequence   int64                      `json:"sequence"`
	Outcome    string                     `json:"outcome"`
	ErrorCode  string                     `json:"error_code,omitempty"`
	RecordedAt time.Time                  `json:"recorded_at"`
	Result     EventOutboxRateLimitStatus `json:"result"`
}

const eventOutboxRateCommandColumns = `command_id::text,sequence,outcome,error_code,recorded_at,result_body,request_digest`

func eventOutboxRateReceipt(row eventIntentScanner) (EventOutboxRateLimitCommandReceipt, []byte, error) {
	var out EventOutboxRateLimitCommandReceipt
	var body string
	var digest []byte
	err := row.Scan(&out.CommandID, &out.Sequence, &out.Outcome, &out.ErrorCode, &out.RecordedAt, &body, &digest)
	if err == nil {
		err = json.Unmarshal([]byte(body), &out.Result)
	}
	return out, digest, err
}

// SetEventOutboxRateLimit fences claim transactions and records a revision and
// idempotent receipt atomically. Existing committed claims continue independently.
func SetEventOutboxRateLimit(ctx context.Context, db *sql.DB, accountID, appID string, options EventOutboxRateLimitOptions) (EventOutboxRateLimitCommandReceipt, error) {
	var zero EventOutboxRateLimitCommandReceipt
	if options.Interval == 0 {
		options.Interval = time.Second
	}
	if options.Burst == 0 {
		options.Burst = 1
	}
	if ctx == nil || db == nil || options.ExpectedRevision < 0 || options.Interval < time.Millisecond || options.Interval > time.Hour || options.Interval%time.Millisecond != 0 || options.Burst < 1 || options.Burst > 10000 {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	commandID := strings.ToLower(options.CommandID)
	if !operationUUID(commandID) {
		return zero, ErrInvalidEventConsumerRequest
	}
	body, err := json.Marshal(struct {
		Version        int
		Revision       int64
		Enabled        bool
		IntervalMillis int64
		Burst          int64
	}{1, options.ExpectedRevision, options.Enabled, options.Interval.Milliseconds(), options.Burst})
	if err != nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	digest := sha256.Sum256(append([]byte("gregale-event-outbox-rate-command-v1\n"), body...))
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, eventOutboxPublishingLock(account, app))
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	saved, stored, err := eventOutboxRateReceipt(tx.QueryRowContext(ctx, `SELECT `+eventOutboxRateCommandColumns+` FROM public.gregale_event_outbox_rate_commands WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if err == nil {
		if !bytes.Equal(stored, digest[:]) {
			return zero, ErrEventOutboxConflict
		}
		return saved, eventOutboxControlError(saved.ErrorCode)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, eventOutboxStoreError(ctx)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.gregale_event_outbox_rate_limits(account_id,app_id) VALUES($1::uuid,$2::uuid) ON CONFLICT(account_id,app_id) DO NOTHING`, account, app)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	previous, err := scanEventOutboxRateBucket(tx.QueryRowContext(ctx, `SELECT `+eventOutboxRateLimitColumns+` FROM public.gregale_event_outbox_rate_limits WHERE account_id=$1::uuid AND app_id=$2::uuid FOR UPDATE`, account, app), account, app)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	applied := previous.status.Revision == options.ExpectedRevision
	interval := options.Interval.Milliseconds()
	credit := interval * options.Burst
	if previous.status.Enabled && options.Enabled {
		credit = min(credit, previous.credit*interval/previous.status.IntervalMillis)
	}
	current, err := scanEventOutboxRateBucket(tx.QueryRowContext(ctx, `UPDATE public.gregale_event_outbox_rate_limits SET enabled=CASE WHEN $3::boolean THEN $4::boolean ELSE enabled END,interval_ms=CASE WHEN $3::boolean THEN $5::bigint ELSE interval_ms END,burst=CASE WHEN $3::boolean THEN $6::bigint ELSE burst END,credit_ms=CASE WHEN $3::boolean THEN $7::bigint ELSE credit_ms END,refilled_at=CASE WHEN $3::boolean THEN $8::timestamptz ELSE refilled_at END,revision=CASE WHEN $3::boolean THEN revision+1 ELSE revision END,updated_at=CASE WHEN $3::boolean THEN clock_timestamp() ELSE updated_at END,last_sequence=last_sequence+1 WHERE account_id=$1::uuid AND app_id=$2::uuid RETURNING `+eventOutboxRateLimitColumns, account, app, applied, options.Enabled, interval, options.Burst, credit, previous.status.ObservedAt), account, app)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	receipt := EventOutboxRateLimitCommandReceipt{CommandID: commandID, Sequence: current.status.LastCommandSequence, Result: current.status, Outcome: "configured"}
	if !options.Enabled {
		receipt.Outcome = "disabled"
	}
	if !applied {
		receipt.Outcome = "rejected"
		receipt.ErrorCode = "conflict"
	}
	result, err := json.Marshal(current.status)
	if err != nil || len(result) > 4096 {
		return zero, ErrEventOutboxStore
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_outbox_rate_commands(account_id,app_id,command_id,request_digest,sequence,outcome,error_code,result_body) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::bigint,$6,$7,$8) RETURNING recorded_at`, account, app, commandID, digest[:], receipt.Sequence, receipt.Outcome, receipt.ErrorCode, string(result)).Scan(&receipt.RecordedAt)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	if err = tx.Commit(); err != nil {
		return zero, errors.Join(ErrEventOutboxCommitUnknown, eventOutboxStoreError(ctx))
	}
	return receipt, eventOutboxControlError(receipt.ErrorCode)
}

// GetEventOutboxRateLimitCommand returns a historical receipt, including rejected
// commands. Retrieval success does not imply that the command was applied.
func GetEventOutboxRateLimitCommand(ctx context.Context, db *sql.DB, accountID, appID, commandID string) (EventOutboxRateLimitCommandReceipt, error) {
	var zero EventOutboxRateLimitCommandReceipt
	if ctx == nil || db == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	commandID = strings.ToLower(commandID)
	if !operationUUID(commandID) {
		return zero, ErrInvalidEventConsumerRequest
	}
	receipt, _, err := eventOutboxRateReceipt(db.QueryRowContext(ctx, `SELECT `+eventOutboxRateCommandColumns+` FROM public.gregale_event_outbox_rate_commands WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrNotFound
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return receipt, nil
}

type EventOutboxRateLimitHistoryOptions struct {
	After int64
	Limit int
}
type EventOutboxRateLimitHistoryPage struct {
	Items     []EventOutboxRateLimitCommandReceipt `json:"items"`
	NextAfter int64                                `json:"next_after,omitempty"`
}

// ListEventOutboxRateLimitCommands lists one scope's retained command history.
// The scope state/counter must survive receipt pruning to preserve sequence order.
func ListEventOutboxRateLimitCommands(ctx context.Context, db *sql.DB, accountID, appID string, options EventOutboxRateLimitHistoryOptions) (EventOutboxRateLimitHistoryPage, error) {
	var out EventOutboxRateLimitHistoryPage
	if ctx == nil || db == nil {
		return out, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return out, err
	}
	if options.Limit == 0 {
		options.Limit = 50
	}
	if options.After < 0 || options.Limit < 1 || options.Limit > 100 {
		return out, ErrInvalidEventConsumerRequest
	}
	rows, err := db.QueryContext(ctx, `SELECT `+eventOutboxRateCommandColumns+` FROM public.gregale_event_outbox_rate_commands WHERE account_id=$1::uuid AND app_id=$2::uuid AND sequence>$3::bigint ORDER BY sequence LIMIT $4::integer`, account, app, options.After, options.Limit+1)
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	defer func() { _ = rows.Close() }()
	out.Items = make([]EventOutboxRateLimitCommandReceipt, 0, options.Limit)
	for rows.Next() {
		receipt, _, scanErr := eventOutboxRateReceipt(rows)
		if scanErr != nil {
			return EventOutboxRateLimitHistoryPage{}, eventOutboxStoreError(ctx)
		}
		if len(out.Items) == options.Limit {
			out.NextAfter = out.Items[len(out.Items)-1].Sequence
			break
		}
		out.Items = append(out.Items, receipt)
	}
	if err := rows.Close(); err != nil {
		return EventOutboxRateLimitHistoryPage{}, eventOutboxStoreError(ctx)
	}
	if rows.Err() != nil {
		return EventOutboxRateLimitHistoryPage{}, eventOutboxStoreError(ctx)
	}
	return out, nil
}
