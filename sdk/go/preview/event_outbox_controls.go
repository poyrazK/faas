package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

// EventOutboxControlReceipt is a retained command result, not a current status
// read. It contains local metadata only, never event payloads or capabilities.
type EventOutboxControlReceipt struct {
	CommandID   string                          `json:"command_id"`
	ProducerKey string                          `json:"producer_key"`
	Action      string                          `json:"action"`
	Sequence    int64                           `json:"sequence"`
	Outcome     string                          `json:"outcome"`
	ErrorCode   string                          `json:"error_code,omitempty"`
	RecordedAt  time.Time                       `json:"recorded_at"`
	Result      EventOutboxReconciliationResult `json:"result"`
}

const eventOutboxControlColumns = `command_id::text,producer_key,action,sequence,outcome,error_code,recorded_at,result_body,request_digest`

func eventOutboxControlReceipt(row eventIntentScanner) (EventOutboxControlReceipt, []byte, error) {
	var out EventOutboxControlReceipt
	var body string
	var digest []byte
	err := row.Scan(&out.CommandID, &out.ProducerKey, &out.Action, &out.Sequence, &out.Outcome, &out.ErrorCode, &out.RecordedAt, &body, &digest)
	if err == nil {
		err = json.Unmarshal([]byte(body), &out.Result)
	}
	return out, digest, err
}
func eventOutboxControlErrorCode(err error) (string, bool) {
	switch {
	case err == nil:
		return "", true
	case errors.Is(err, ErrEventOutboxConflict):
		return "conflict", true
	case errors.Is(err, ErrEventOutboxUnconfirmed):
		return "unconfirmed", true
	case errors.Is(err, ErrEventOutboxBusy):
		return "busy", true
	case errors.Is(err, faas.ErrNotFound):
		return "not_found", true
	default:
		return "", false
	}
}
func eventOutboxControlError(code string) error {
	switch code {
	case "":
		return nil
	case "conflict":
		return ErrEventOutboxConflict
	case "unconfirmed":
		return ErrEventOutboxUnconfirmed
	case "busy":
		return ErrEventOutboxBusy
	case "not_found":
		return faas.ErrNotFound
	default:
		return ErrEventOutboxStore
	}
}

// Command transactions serialize only an identical owner/command ID across HTTP.
// They do not hold publication-row or sequence-counter locks during verification.
func runEventOutboxControl(ctx context.Context, db *sql.DB, accountID, appID, key, commandID, action string, request any, execute func(*sql.Tx) (EventOutboxReconciliationResult, error)) (EventOutboxControlReceipt, error) {
	var zero EventOutboxControlReceipt
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	commandID = strings.ToLower(commandID)
	if !operationUUID(commandID) || !eventOutboxKey(key) {
		return zero, ErrInvalidEventConsumerRequest
	}
	body, err := json.Marshal(struct {
		Action  string `json:"action"`
		Key     string `json:"key"`
		Options any    `json:"options"`
	}{action, key, request})
	if err != nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	digest := sha256.Sum256(append([]byte("gregale-event-outbox-control-v1\n"), body...))
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "gregale.event-outbox-control.v1:"+account+":"+app+":"+commandID)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	saved, savedDigest, err := eventOutboxControlReceipt(tx.QueryRowContext(ctx, `SELECT `+eventOutboxControlColumns+` FROM public.gregale_event_outbox_controls WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if err == nil {
		if saved.Action != action || saved.ProducerKey != key || !bytes.Equal(savedDigest, digest[:]) {
			return zero, ErrEventOutboxConflict
		}
		return saved, eventOutboxControlError(saved.ErrorCode)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, eventOutboxStoreError(ctx)
	}
	result, actionErr := execute(tx)
	code, recordable := eventOutboxControlErrorCode(actionErr)
	if !recordable || ctx.Err() != nil {
		return zero, errors.Join(actionErr, ctx.Err())
	}
	if result.Outcome == "" {
		result.Outcome = "unverified"
		if action == "resume" || action == "set_retry_policy" {
			result.Outcome = "rejected"
		}
	}
	resultBody, err := json.Marshal(result)
	if err != nil || len(resultBody) > 8192 {
		return zero, ErrEventOutboxStore
	}
	receipt := EventOutboxControlReceipt{CommandID: commandID, ProducerKey: key, Action: action, Outcome: result.Outcome, ErrorCode: code, Result: result}
	// This transactional counter serializes per-key receipt commit order. Retaining
	// it across history pruning avoids sequence reuse and late-commit pagination gaps.
	err = tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_outbox_control_sequences(account_id,app_id,producer_key,last_sequence) VALUES($1::uuid,$2::uuid,$3,1) ON CONFLICT(account_id,app_id,producer_key) DO UPDATE SET last_sequence=gregale_event_outbox_control_sequences.last_sequence+1 RETURNING last_sequence`, account, app, key).Scan(&receipt.Sequence)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO public.gregale_event_outbox_controls(account_id,app_id,command_id,producer_key,action,request_digest,sequence,outcome,error_code,result_body) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7::bigint,$8,$9,$10) RETURNING recorded_at`, account, app, commandID, key, action, digest[:], receipt.Sequence, receipt.Outcome, code, string(resultBody)).Scan(&receipt.RecordedAt)
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	if err = tx.Commit(); err != nil {
		return zero, errors.Join(ErrEventOutboxCommitUnknown, eventOutboxStoreError(ctx))
	}
	return receipt, eventOutboxControlError(code)
}

func reconcileEventOutboxControl(ctx context.Context, db *sql.DB, client *Client, accountID, appID, key string, options EventOutboxReconciliationOptions) (EventOutboxReconciliationResult, error) {
	if options.RequestTimeout == 0 {
		options.RequestTimeout = 10 * time.Second
	}
	if options.RequestTimeout < time.Millisecond || options.RequestTimeout > 30*time.Second || options.AppSlug != "" && !eventOutboxSlug(options.AppSlug) {
		return EventOutboxReconciliationResult{}, ErrInvalidEventConsumerRequest
	}
	receipt, err := runEventOutboxControl(ctx, db, accountID, appID, key, options.CommandID, "reconcile", struct {
		AppSlug string `json:"app_slug"`
	}{options.AppSlug}, func(tx *sql.Tx) (EventOutboxReconciliationResult, error) {
		return reconcileEventOutbox(ctx, tx, client, accountID, appID, key, options)
	})
	return receipt.Result, err
}
func resumeEventOutboxControl(ctx context.Context, db *sql.DB, accountID, appID, key string, options EventOutboxResumeOptions) (EventOutboxStatus, error) {
	if options.ExpectedUpdatedAt.IsZero() || options.ExpectedAttempts < 0 || options.AppSlug != "" && !eventOutboxSlug(options.AppSlug) {
		return EventOutboxStatus{}, ErrInvalidEventConsumerRequest
	}
	request := struct {
		AppSlug           string    `json:"app_slug"`
		ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
		ExpectedAttempts  int64     `json:"expected_attempts"`
	}{options.AppSlug, options.ExpectedUpdatedAt.UTC(), options.ExpectedAttempts}
	receipt, err := runEventOutboxControl(ctx, db, accountID, appID, key, options.CommandID, "resume", request, func(tx *sql.Tx) (EventOutboxReconciliationResult, error) {
		status, resumeErr := resumeEventOutbox(ctx, tx, accountID, appID, key, options)
		outcome := "resumed"
		if resumeErr != nil {
			outcome = "rejected"
		}
		return EventOutboxReconciliationResult{Outcome: outcome, Publication: status}, resumeErr
	})
	return receipt.Result.Publication, err
}

// GetEventOutboxControlReceipt retrieves a retained result even if its command
// returned an error or its publication was pruned. Inspect ErrorCode and Result;
// successful retrieval does not mean the control succeeded.
func GetEventOutboxControlReceipt(ctx context.Context, db *sql.DB, accountID, appID, commandID string) (EventOutboxControlReceipt, error) {
	var zero EventOutboxControlReceipt
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
	receipt, _, err := eventOutboxControlReceipt(db.QueryRowContext(ctx, `SELECT `+eventOutboxControlColumns+` FROM public.gregale_event_outbox_controls WHERE account_id=$1::uuid AND app_id=$2::uuid AND command_id=$3::uuid`, account, app, commandID))
	if errors.Is(err, sql.ErrNoRows) {
		return zero, faas.ErrNotFound
	}
	if err != nil {
		return zero, eventOutboxStoreError(ctx)
	}
	return receipt, nil
}
