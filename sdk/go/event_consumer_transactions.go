package faas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// EventConsumerReceiptSchema is application-owned PostgreSQL DDL. Install it
// explicitly as the database owner. The helper never installs or prunes it.
//
//go:embed event_consumer_schema.sql
var EventConsumerReceiptSchema string

var (
	ErrInvalidEventConsumerRequest  = errors.New("invalid event consumer request")
	ErrEventConsumerReceiptConflict = errors.New("event consumer receipt scope or content differs")
	ErrEventConsumerCommitUnknown   = errors.New("event consumer commit outcome unknown; retry with the same acceptance and consumer")
)

// EventConsumerIdentity identifies one consumer of one durable acceptance.
// AcceptanceID is the positive decimal x-gregale-event-acceptance-id, not the
// invocation ID or a retry generation. A new acceptance after retention expiry
// has a new identity even when Source and EventID are reused.
type EventConsumerIdentity struct {
	AccountID      string
	AppID          string
	SubscriptionID string
	AcceptanceID   int64
	Source         string
	EventID        string
}

// EventConsumerRequest is a validated immutable request. Construct it only from
// authenticated delivery context; the constructors do not authenticate callers.
type EventConsumerRequest struct {
	identity EventConsumerIdentity
	body     []byte
	valid    bool
}

// NewEventConsumerRequest accepts the original CloudEvents body bytes. Retries
// must preserve those bytes; volatile attempt and trace headers are not input.
func NewEventConsumerRequest(identity EventConsumerIdentity, body []byte) (EventConsumerRequest, error) {
	identity.AccountID = strings.ToLower(identity.AccountID)
	identity.AppID = strings.ToLower(identity.AppID)
	identity.SubscriptionID = strings.ToLower(identity.SubscriptionID)
	if !operationUUID(identity.AccountID) || !operationUUID(identity.AppID) || !operationUUID(identity.SubscriptionID) || identity.AcceptanceID <= 0 || len(body) == 0 || len(body) > eventConsumerRequestMaxBytes {
		return EventConsumerRequest{}, ErrInvalidEventConsumerRequest
	}
	for _, value := range []string{identity.Source, identity.EventID} {
		if len(value) == 0 || len(value) > eventConsumerIdentityMaxBytes || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
			return EventConsumerRequest{}, ErrInvalidEventConsumerRequest
		}
	}
	var envelope struct {
		SpecVersion string          `json:"specversion"`
		Source      string          `json:"source"`
		ID          string          `json:"id"`
		Type        string          `json:"type"`
		Data        json.RawMessage `json:"data"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &envelope) != nil || envelope.SpecVersion != "1.0" || envelope.Source != identity.Source || envelope.ID != identity.EventID || envelope.Type == "" || len(envelope.Data) == 0 {
		return EventConsumerRequest{}, ErrInvalidEventConsumerRequest
	}
	return EventConsumerRequest{identity: identity, body: bytes.Clone(body), valid: true}, nil
}

// EventConsumerRequestFromHTTP uses account/app scope supplied by the owner and
// event headers supplied by authenticated internal delivery. These headers are
// not signatures: authenticate the route before calling this function. Do not
// use it to authorize arbitrary public HTTP requests or direct inbox messages.
func EventConsumerRequestFromHTTP(r *http.Request, body []byte, accountID, appID string) (EventConsumerRequest, error) {
	if r == nil {
		return EventConsumerRequest{}, ErrInvalidEventConsumerRequest
	}
	values := make([]string, 4)
	for i, name := range []string{"X-Gregale-Event-Subscription-Id", "X-Gregale-Event-Acceptance-Id", "X-Gregale-Event-Source", "X-Gregale-Event-Id"} {
		var list []string
		for key, entries := range r.Header {
			if strings.EqualFold(key, name) {
				list = append(list, entries...)
			}
		}
		if len(list) != 1 || list[0] == "" {
			return EventConsumerRequest{}, ErrInvalidEventConsumerRequest
		}
		values[i] = list[0]
	}
	acceptance, err := strconv.ParseInt(values[1], 10, 64)
	if err != nil || acceptance <= 0 || strconv.FormatInt(acceptance, 10) != values[1] {
		return EventConsumerRequest{}, ErrInvalidEventConsumerRequest
	}
	return NewEventConsumerRequest(EventConsumerIdentity{AccountID: accountID, AppID: appID, SubscriptionID: values[0], AcceptanceID: acceptance, Source: values[2], EventID: values[3]}, body)
}

// EventConsumerSQLTransaction exposes business queries without commit/rollback.
type EventConsumerSQLTransaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type EventConsumerTransactionResult struct {
	Body     json.RawMessage
	Replayed bool
}

// WithEventConsumerTransaction owns a fresh READ COMMITTED transaction. The
// callback must use only the supplied transaction, must not control its lifecycle,
// and must not perform external effects. It returns a JSON result up to 1 MiB.
// Commit the receipt with business writes before acknowledging the delivery.
// Concurrent duplicates wait and replay saved bytes without calling handler.
func WithEventConsumerTransaction(ctx context.Context, db *sql.DB, input EventConsumerRequest, handler func(EventConsumerSQLTransaction) (json.RawMessage, error)) (EventConsumerTransactionResult, error) {
	return withEventConsumerTransaction(ctx, db, input, handler, nil)
}

func withEventConsumerTransaction(ctx context.Context, db *sql.DB, input EventConsumerRequest, handler func(EventConsumerSQLTransaction) (json.RawMessage, error), lease *EventConsumerProcessingLease, hooks ...func(context.Context, *sql.Tx, EventConsumerTransactionResult) error) (EventConsumerTransactionResult, error) {
	if !input.valid || db == nil || handler == nil {
		return EventConsumerTransactionResult{}, ErrInvalidEventConsumerRequest
	}
	id := input.identity
	identityDigest, requestDigest := eventConsumerDigests(input)

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return EventConsumerTransactionResult{}, fmt.Errorf("event consumer transaction begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best effort after errors or commit
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "gregale.event-consumer-inbox.v1:"+hex.EncodeToString(identityDigest[:])); err != nil {
		return EventConsumerTransactionResult{}, fmt.Errorf("event consumer transaction lock: %w", err)
	}
	var account, app, subscription, source, eventID, response string
	var acceptance int64
	var storedDigest []byte
	err = tx.QueryRowContext(ctx, `SELECT account_id::text, app_id::text, subscription_id::text, acceptance_id, event_source, event_id, request_digest, response_body FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1`, identityDigest[:]).Scan(&account, &app, &subscription, &acceptance, &source, &eventID, &storedDigest, &response)
	replayed := err == nil
	if replayed {
		if account != id.AccountID || app != id.AppID || subscription != id.SubscriptionID || acceptance != id.AcceptanceID || source != id.Source || eventID != id.EventID || !bytes.Equal(storedDigest, requestDigest[:]) {
			return EventConsumerTransactionResult{}, ErrEventConsumerReceiptConflict
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if lease != nil {
			if err := lease.check(ctx, tx); err != nil {
				return EventConsumerTransactionResult{}, err
			}
		}
		body, err := handler(tx)
		if err != nil {
			return EventConsumerTransactionResult{}, err
		}
		response = string(body)
		if !validEventConsumerResponse(response) {
			return EventConsumerTransactionResult{}, ErrInvalidEventConsumerRequest
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.gregale_event_consumer_inbox (identity_digest,account_id,app_id,subscription_id,acceptance_id,event_source,event_id,request_digest,response_body) VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9)`, identityDigest[:], id.AccountID, id.AppID, id.SubscriptionID, id.AcceptanceID, id.Source, id.EventID, requestDigest[:], response); err != nil {
			return EventConsumerTransactionResult{}, fmt.Errorf("event consumer receipt insert: %w", err)
		}
	} else {
		return EventConsumerTransactionResult{}, fmt.Errorf("event consumer receipt read: %w", err)
	}
	if !validEventConsumerResponse(response) {
		return EventConsumerTransactionResult{}, ErrInvalidEventConsumerRequest
	}
	if lease != nil && !replayed {
		if err := lease.release(ctx, tx); err != nil {
			return EventConsumerTransactionResult{}, err
		}
	}
	result := EventConsumerTransactionResult{Body: json.RawMessage(response), Replayed: replayed}
	for _, hook := range hooks {
		if hook != nil {
			if err := hook(ctx, tx, result); err != nil {
				return EventConsumerTransactionResult{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return EventConsumerTransactionResult{}, fmt.Errorf("%w: %w", ErrEventConsumerCommitUnknown, err)
	}
	return result, nil
}

func validEventConsumerResponse(body string) bool {
	return len(body) > 0 && len(body) <= eventConsumerResultMaxBytes && utf8.ValidString(body) && !strings.ContainsRune(body, '\x00') && json.Valid([]byte(body))
}

func eventConsumerDigests(input EventConsumerRequest) ([32]byte, [32]byte) {
	id := input.identity
	frame := "gregale.event-consumer.v1\n" + id.AccountID + "\n" + id.AppID + "\n" + id.SubscriptionID + "\n" + strconv.FormatInt(id.AcceptanceID, 10)
	return sha256.Sum256([]byte(frame)), sha256.Sum256(append([]byte("gregale.event-consumer-input.v1\n"), input.body...))
}
