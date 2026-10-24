package preview

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

	faas "github.com/poyrazK/faas/sdk/go"
)

// EventOutboxPublishingControlSchema adds application-owned pause/history tables.
// Install explicitly before upgrading claim workers and health readers.
//
//go:embed event_outbox_publishing_control_schema.sql
var EventOutboxPublishingControlSchema string

var ErrEventOutboxPublishingPaused = errors.New("event outbox publishing is paused")

// EventOutboxPublishingControl is current scope metadata, not a historical result.
// Revision zero and nil UpdatedAt describe the initial enabled state.
type EventOutboxPublishingControl struct {
	AccountID           string     `json:"account_id"`
	AppID               string     `json:"app_id"`
	Paused              bool       `json:"paused"`
	Revision            int64      `json:"revision"`
	Reason              string     `json:"reason,omitempty"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
	LastCommandSequence int64      `json:"last_command_sequence"`
}
type EventOutboxPublishingControlOptions struct {
	CommandID        string
	ExpectedRevision int64
	Paused           bool
	Reason           string // Optional 0..128 byte lowercase ASCII reason code, not free text.
}
type EventOutboxPublishingCommandReceipt struct {
	CommandID  string                       `json:"command_id"`
	Sequence   int64                        `json:"sequence"`
	Outcome    string                       `json:"outcome"`
	ErrorCode  string                       `json:"error_code,omitempty"`
	RecordedAt time.Time                    `json:"recorded_at"`
	Result     EventOutboxPublishingControl `json:"result"`
}

const eventOutboxPublishingStatusColumns = `paused,revision,reason,updated_at,last_sequence`
const eventOutboxPublishingCommandColumns = `command_id::text,sequence,outcome,error_code,recorded_at,result_body,request_digest`

func eventOutboxPublishingLock(account, app string) string {
	return "gregale.event-outbox-publishing.v1:" + account + ":" + app
}
func eventOutboxPublishingStatus(row eventIntentScanner, account, app string) (EventOutboxPublishingControl, error) {
	out := EventOutboxPublishingControl{AccountID: account, AppID: app}
	var updated sql.NullTime
	err := row.Scan(&out.Paused, &out.Revision, &out.Reason, &updated, &out.LastCommandSequence)
	if updated.Valid {
		out.UpdatedAt = &updated.Time
	}
	return out, err
}
func eventOutboxPublishingReceipt(row eventIntentScanner) (EventOutboxPublishingCommandReceipt, []byte, error) {
	var out EventOutboxPublishingCommandReceipt
	var body string
	var digest []byte
	err := row.Scan(&out.CommandID, &out.Sequence, &out.Outcome, &out.ErrorCode, &out.RecordedAt, &body, &digest)
	if err == nil {
		err = json.Unmarshal([]byte(body), &out.Result)
	}
	return out, digest, err
}

// GetEventOutboxPublishingControl reads without installing state. A missing scope
// row means enabled revision zero; unavailable storage is an error, not enabled.
func GetEventOutboxPublishingControl(ctx context.Context, db *sql.DB, accountID, appID string) (EventOutboxPublishingControl, error) {
	var zero EventOutboxPublishingControl
	if ctx == nil || db == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	out, err := eventOutboxPublishingStatus(db.QueryRowContext(ctx, `SELECT `+eventOutboxPublishingStatusColumns+` FROM public.gregale_event_outbox_publishing_controls WHERE account_id=$1::uuid AND app_id=$2::uuid`, account, app), account, app)
	if errors.Is(err, sql.ErrNoRows) {
		return EventOutboxPublishingControl{AccountID: account, AppID: app}, nil
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return out, nil
}

// SetEventOutboxPublishingPaused atomically records an enabled/paused revision
// and command receipt. A pause fences new claims, not already committed claims.
// Replays return the original receipt; changing parameters with the ID conflicts.
func SetEventOutboxPublishingPaused(ctx context.Context, db *sql.DB, accountID, appID string, options EventOutboxPublishingControlOptions) (EventOutboxPublishingCommandReceipt, error) {
	var zero EventOutboxPublishingCommandReceipt
	if ctx == nil || db == nil || options.ExpectedRevision < 0 || len(options.Reason) > 128 {
		return zero, ErrInvalidEventConsumerRequest
	}
	for _, b := range []byte(options.Reason) {
		if b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '.' {
			continue
		}
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
		Version  int    `json:"version"`
		Revision int64  `json:"expected_revision"`
		Paused   bool   `json:"paused"`
		Reason   string `json:"reason"`
	}{1, options.ExpectedRevision, options.Paused, options.Reason})
	if err != nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	digest := sha256.Sum256(append([]byte("gregale-event-outbox-publishing-command-v1\n"), body...))
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	// Exclusive scope lock waits only for claim transactions, never their HTTP.
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, eventOutboxPublishingLock(account, app))
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	saved, stored, err := eventOutboxPublishingReceipt(tx.QueryRowContext(ctx, `SELECT `+eventOutboxPublishingCommandColumns+` FROM public.gregale_event_outbox_publishing_commands WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if err == nil {
		if !bytes.Equal(stored, digest[:]) {
			return zero, ErrEventOutboxConflict
		}
		return saved, eventOutboxControlError(saved.ErrorCode)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, eventOutboxStoreError(ctx)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.gregale_event_outbox_publishing_controls(account_id,app_id) VALUES($1::uuid,$2::uuid) ON CONFLICT(account_id,app_id) DO NOTHING`, account, app)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	previous, err := eventOutboxPublishingStatus(tx.QueryRowContext(ctx, `SELECT `+eventOutboxPublishingStatusColumns+` FROM public.gregale_event_outbox_publishing_controls WHERE account_id=$1::uuid AND app_id=$2::uuid FOR UPDATE`, account, app), account, app)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	applied := previous.Revision == options.ExpectedRevision
	current, err := eventOutboxPublishingStatus(tx.QueryRowContext(ctx, `UPDATE public.gregale_event_outbox_publishing_controls SET paused=CASE WHEN $3::boolean THEN $4::boolean ELSE paused END,reason=CASE WHEN $3::boolean THEN $5::text ELSE reason END,revision=CASE WHEN $3::boolean THEN revision+1 ELSE revision END,updated_at=CASE WHEN $3::boolean THEN clock_timestamp() ELSE updated_at END,last_sequence=last_sequence+1 WHERE account_id=$1::uuid AND app_id=$2::uuid RETURNING `+eventOutboxPublishingStatusColumns, account, app, applied, options.Paused, options.Reason), account, app)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	receipt := EventOutboxPublishingCommandReceipt{CommandID: commandID, Sequence: current.LastCommandSequence, Result: current, Outcome: "resumed"}
	if options.Paused {
		receipt.Outcome = "paused"
	}
	if !applied {
		receipt.Outcome = "rejected"
		receipt.ErrorCode = "conflict"
	}
	result, err := json.Marshal(current)
	if err != nil || len(result) > 4096 {
		return zero, ErrEventOutboxStore
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_outbox_publishing_commands(account_id,app_id,command_id,request_digest,sequence,outcome,error_code,result_body) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::bigint,$6,$7,$8) RETURNING recorded_at`, account, app, commandID, digest[:], receipt.Sequence, receipt.Outcome, receipt.ErrorCode, string(result)).Scan(&receipt.RecordedAt)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	if err = tx.Commit(); err != nil {
		return zero, errors.Join(ErrEventOutboxCommitUnknown, eventOutboxStoreError(ctx))
	}
	return receipt, eventOutboxControlError(receipt.ErrorCode)
}

// GetEventOutboxPublishingCommand returns a historical result, including rejected
// commands. Retrieval success does not imply that the command was applied.
func GetEventOutboxPublishingCommand(ctx context.Context, db *sql.DB, accountID, appID, commandID string) (EventOutboxPublishingCommandReceipt, error) {
	var zero EventOutboxPublishingCommandReceipt
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
	receipt, _, err := eventOutboxPublishingReceipt(db.QueryRowContext(ctx, `SELECT `+eventOutboxPublishingCommandColumns+` FROM public.gregale_event_outbox_publishing_commands WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, faas.ErrNotFound
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return receipt, nil
}

type EventOutboxPublishingHistoryOptions struct {
	After int64
	Limit int
}
type EventOutboxPublishingHistoryPage struct {
	Items     []EventOutboxPublishingCommandReceipt `json:"items"`
	NextAfter int64                                 `json:"next_after,omitempty"`
}

// ListEventOutboxPublishingCommands lists one scope's retained command history.
// The scope state/counter must survive receipt pruning to preserve sequence order.
func ListEventOutboxPublishingCommands(ctx context.Context, db *sql.DB, accountID, appID string, options EventOutboxPublishingHistoryOptions) (EventOutboxPublishingHistoryPage, error) {
	var out EventOutboxPublishingHistoryPage
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
	rows, err := db.QueryContext(ctx, `SELECT `+eventOutboxPublishingCommandColumns+` FROM public.gregale_event_outbox_publishing_commands WHERE account_id=$1::uuid AND app_id=$2::uuid AND sequence>$3::bigint ORDER BY sequence LIMIT $4::integer`, account, app, options.After, options.Limit+1)
	if err != nil {
		return out, eventOutboxStoreError(ctx)
	}
	defer func() { _ = rows.Close() }()
	out.Items = make([]EventOutboxPublishingCommandReceipt, 0, options.Limit)
	for rows.Next() {
		receipt, _, scanErr := eventOutboxPublishingReceipt(rows)
		if scanErr != nil {
			return EventOutboxPublishingHistoryPage{}, eventOutboxStoreError(ctx)
		}
		if len(out.Items) == options.Limit {
			out.NextAfter = out.Items[len(out.Items)-1].Sequence
			break
		}
		out.Items = append(out.Items, receipt)
	}
	if err := rows.Close(); err != nil {
		return EventOutboxPublishingHistoryPage{}, eventOutboxStoreError(ctx)
	}
	if rows.Err() != nil {
		return EventOutboxPublishingHistoryPage{}, eventOutboxStoreError(ctx)
	}
	return out, nil
}
