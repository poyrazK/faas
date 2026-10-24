package faas

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrEventAcknowledgmentLeaseLost   = errors.New("event acknowledgment lease expired, replaced or unavailable")
	ErrEventAcknowledgmentUnconfirmed = errors.New("event acknowledgment callback outcome unconfirmed")
	ErrEventAcknowledgmentWorkerBusy  = errors.New("event acknowledgment worker operation already running")
	ErrEventConsumerReceiptMissing    = errors.New("no committed event consumer receipt")
	ErrEventConsumerReceiptCommitted  = errors.New("event consumer receipt already committed; complete instead of rejecting")
)

// Options bound helper RPC calls; the client's own transport may also retry.
// Zero values select three attempts, a lease-relative timeout, and a short backoff.
type EventAcknowledgmentWorkerOptions struct {
	CallbackAttempts int
	CallbackTimeout  time.Duration
	RetryBackoff     time.Duration
}

// ReceiptConfirmed means the local receipt committed, even if acknowledgment fails.
// False does not prove rollback: ErrEventConsumerCommitUnknown requires reconciliation.
type EventAcknowledgmentWorkerResult struct {
	Receipt          EventConsumerTransactionResult
	ReceiptConfirmed bool
	Acknowledgment   EventAcknowledgmentStatus
}

// EventAcknowledgmentWorker contains an opaque capability. Copies share an operation
// guard; reconstruct from the protected durable job after a process restart.
type EventAcknowledgmentWorker struct{ state *eventAcknowledgmentWorker }
type eventAcknowledgmentWorker struct {
	mu       sync.Mutex
	client   *Client
	app      string
	input    EventConsumerRequest
	delivery EventConsumerDeliveryContext
	token    string
	seconds  int
	options  EventAcknowledgmentWorkerOptions
}

// EventAcknowledgmentWorkerFromHTTP requires an authenticated internal delivery.
// Headers are not signatures. app is the API slug; accountID/appID are owner scope.
// Save the original body and required headers as secrets in a durable job before 202.
func EventAcknowledgmentWorkerFromHTTP(client *Client, app string, r *http.Request, body []byte, accountID, appID string, options EventAcknowledgmentWorkerOptions) (*EventAcknowledgmentWorker, error) {
	if client == nil || client.Client == nil || app == "" || r == nil {
		return nil, ErrInvalidEventConsumerRequest
	}
	input, err := EventConsumerRequestFromHTTP(r, body, accountID, appID)
	if err != nil {
		return nil, err
	}
	delivery, err := EventConsumerDeliveryContextFromHTTP(r, body, accountID, appID)
	if err != nil {
		return nil, err
	}
	token, ok := eventWorkerHeader(r, "X-Gregale-Event-Acknowledgment-Token")
	if len(token) != 64 {
		return nil, ErrInvalidEventConsumerRequest
	}
	decoded, err := hex.DecodeString(token)
	if !ok || err != nil || len(decoded) != 32 || token != strings.ToLower(token) {
		return nil, ErrInvalidEventConsumerRequest
	}
	raw, ok := eventWorkerHeader(r, "X-Gregale-Event-Acknowledgment-Lease-Seconds")
	if len(raw) < 1 || len(raw) > 3 {
		return nil, ErrInvalidEventConsumerRequest
	}
	seconds := 0
	for _, c := range []byte(raw) {
		if c < '0' || c > '9' || seconds > 300 {
			return nil, ErrInvalidEventConsumerRequest
		}
		seconds = seconds*10 + int(c-'0')
	}
	if !ok || seconds < 1 || seconds > 300 || fmt.Sprint(seconds) != raw {
		return nil, ErrInvalidEventConsumerRequest
	}
	options, err = eventWorkerOptions(options, seconds)
	if err != nil {
		return nil, err
	}
	return &EventAcknowledgmentWorker{state: &eventAcknowledgmentWorker{client: client, app: app, input: input, delivery: delivery, token: token, seconds: seconds, options: options}}, nil
}
func eventWorkerHeader(r *http.Request, name string) (string, bool) {
	var value string
	count := 0
	for key, values := range r.Header {
		if strings.EqualFold(key, name) {
			if len(values) != 1 {
				return "", false
			}
			count++
			value = values[0]
		}
	}
	return value, count == 1 && value != ""
}
func eventWorkerOptions(o EventAcknowledgmentWorkerOptions, seconds int) (EventAcknowledgmentWorkerOptions, error) {
	interval := time.Duration(seconds) * time.Second / 3
	if o.CallbackAttempts == 0 {
		o.CallbackAttempts = 3
	}
	if o.CallbackTimeout == 0 {
		o.CallbackTimeout = min(5*time.Second, interval/2)
	}
	if o.RetryBackoff == 0 {
		o.RetryBackoff = min(100*time.Millisecond, interval/4)
	}
	if o.CallbackAttempts < 1 || o.CallbackAttempts > 10 || o.CallbackTimeout < time.Millisecond || o.CallbackTimeout > min(5*time.Second, interval/2) || o.RetryBackoff < time.Millisecond || o.RetryBackoff > interval {
		return o, ErrInvalidEventConsumerRequest
	}
	return o, nil
}
func (w EventAcknowledgmentWorker) String() string               { return "EventAcknowledgmentWorker{redacted}" }
func (w EventAcknowledgmentWorker) GoString() string             { return w.String() }
func (w EventAcknowledgmentWorker) MarshalJSON() ([]byte, error) { return json.Marshal(w.String()) }

func (w *EventAcknowledgmentWorker) lock(ctx context.Context, db *sql.DB) (*eventAcknowledgmentWorker, error) {
	if w == nil || w.state == nil || ctx == nil || db == nil {
		return nil, ErrInvalidEventConsumerRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !w.state.mu.TryLock() {
		return nil, ErrEventAcknowledgmentWorkerBusy
	}
	return w.state, nil
}

// Run calls business code at most once in this call, inside the existing receipt
// transaction. The handler must use its context and transaction, perform no external
// effects, and never commit/rollback. A committed receipt skips business code.
func (w *EventAcknowledgmentWorker) Run(ctx context.Context, db *sql.DB, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentWorkerResult, error) {
	return w.run(ctx, db, nil, handler)
}

// RunWithProcessingLease also fences the existing application-owned preparation
// lease. Its duration is not extended here: Complete locks that row during business
// writes and checks expiry before commit. Keep this final transaction brief.
func (w *EventAcknowledgmentWorker) RunWithProcessingLease(ctx context.Context, db *sql.DB, lease EventConsumerProcessingLease, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentWorkerResult, error) {
	return w.run(ctx, db, &lease, handler)
}
func (w *EventAcknowledgmentWorker) run(ctx context.Context, db *sql.DB, lease *EventConsumerProcessingLease, handler func(context.Context, EventConsumerSQLTransaction) (json.RawMessage, error)) (EventAcknowledgmentWorkerResult, error) {
	var out EventAcknowledgmentWorkerResult
	s, err := w.lock(ctx, db)
	if err != nil {
		return out, err
	}
	defer s.mu.Unlock()
	if handler == nil {
		return out, ErrInvalidEventConsumerRequest
	}
	if lease != nil {
		identity, digest := eventConsumerDigests(s.input)
		other, body := eventConsumerDigests(lease.input)
		if !lease.valid || identity != other || digest != body {
			return out, ErrInvalidEventConsumerRequest
		}
	}
	receipt, err := s.receipt(ctx, db)
	if err == nil {
		return s.complete(ctx, receipt)
	}
	if !errors.Is(err, ErrEventConsumerReceiptMissing) {
		return out, err
	}
	if err := s.startLease(ctx); err != nil {
		return out, err
	}
	workCtx, cancelWork := context.WithCancelCause(ctx)
	defer cancelWork(context.Canceled)
	stop := s.monitor(workCtx, cancelWork)
	defer stop()
	callback := func(tx EventConsumerSQLTransaction) (json.RawMessage, error) { return handler(workCtx, tx) }
	if lease == nil {
		receipt, err = WithEventConsumerTransaction(workCtx, db, s.input, callback)
	} else {
		receipt, err = lease.Complete(workCtx, db, callback)
	}
	stop()
	cause := context.Cause(workCtx)
	if err != nil {
		return out, errors.Join(err, cause)
	}
	// Local commit is confirmed. Even if the last renewal was uncertain, the
	// terminal callback is safe to try; server fencing decides whether it can apply.
	out, err = s.complete(ctx, receipt)
	if err != nil {
		return out, errors.Join(err, cause)
	}
	return out, nil
}

// CompleteReceipt reconciles a prior commit/acknowledgment uncertainty without
// invoking business code. Missing or conflicting receipts never send completion.
func (w *EventAcknowledgmentWorker) CompleteReceipt(ctx context.Context, db *sql.DB) (EventAcknowledgmentWorkerResult, error) {
	var out EventAcknowledgmentWorkerResult
	s, err := w.lock(ctx, db)
	if err != nil {
		return out, err
	}
	defer s.mu.Unlock()
	receipt, err := s.receipt(ctx, db)
	if err != nil {
		return out, err
	}
	return s.complete(ctx, receipt)
}
func (s *eventAcknowledgmentWorker) receipt(ctx context.Context, db *sql.DB) (EventConsumerTransactionResult, error) {
	// Reuse all existing receipt scope/digest checks. This sentinel callback does
	// no business work; absent receipts roll back without creating an inbox entry.
	return WithEventConsumerTransaction(ctx, db, s.input, func(EventConsumerSQLTransaction) (json.RawMessage, error) { return nil, ErrEventConsumerReceiptMissing })
}
func (s *eventAcknowledgmentWorker) complete(ctx context.Context, receipt EventConsumerTransactionResult) (EventAcknowledgmentWorkerResult, error) {
	out := EventAcknowledgmentWorkerResult{Receipt: receipt, ReceiptConfirmed: true}
	body := append(json.RawMessage(nil), receipt.Body...)
	ack, err := s.terminal(ctx, "complete", "", body)
	out.Acknowledgment = ack
	return out, err
}

// Retry requires stopped work and a known rollback, never an unknown commit.
// A discovered committed receipt prevents rejection. This is not a distributed
// lock against other worker objects: use exclusive durable-job ownership.
func (w *EventAcknowledgmentWorker) Retry(ctx context.Context, db *sql.DB, reason string) (EventAcknowledgmentWorkerResult, error) {
	return w.reject(ctx, db, "retry", reason)
}

// DeadLetter has the same ownership and receipt requirements as Retry.
func (w *EventAcknowledgmentWorker) DeadLetter(ctx context.Context, db *sql.DB, reason string) (EventAcknowledgmentWorkerResult, error) {
	return w.reject(ctx, db, "dead-letter", reason)
}
func (w *EventAcknowledgmentWorker) reject(ctx context.Context, db *sql.DB, action, reason string) (EventAcknowledgmentWorkerResult, error) {
	var out EventAcknowledgmentWorkerResult
	s, err := w.lock(ctx, db)
	if err != nil {
		return out, err
	}
	defer s.mu.Unlock()
	if !eventWorkerReason(reason) {
		return out, ErrInvalidEventConsumerRequest
	}
	receipt, err := s.receipt(ctx, db)
	if err == nil {
		return EventAcknowledgmentWorkerResult{Receipt: receipt, ReceiptConfirmed: true}, ErrEventConsumerReceiptCommitted
	}
	if !errors.Is(err, ErrEventConsumerReceiptMissing) {
		return out, err
	}
	out.Acknowledgment, err = s.terminal(ctx, action, reason, nil)
	return out, err
}
func eventWorkerReason(reason string) bool {
	if len(reason) == 0 || len(reason) > 128 {
		return false
	}
	for _, c := range []byte(reason) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '.' && c != '-' {
			return false
		}
	}
	return true
}
