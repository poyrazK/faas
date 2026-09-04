package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// AuditEventOutbox is one durable cross-daemon audit delivery record.
//
// The outbox is deliberately an optional Store capability rather than part of
// Store itself. A number of small test and integration stores implement only a
// narrow subset of Store; keeping this seam additive lets those callers keep
// compiling while PgStore and MemStore gain the release-readiness primitive.
type AuditEventOutbox struct {
	ID        int64
	Actor     string
	Kind      string
	Subject   *string
	Data      []byte
	DedupeKey string
	Attempts  int
}

// AuditEventOutboxStore is the durable queue used by imaged to hand signature
// audit events to apid. pg_notify remains the low-latency wakeup, but a missed
// notification no longer loses the audit row: apid claims and replays rows
// from this store until delivery succeeds or the row reaches the dead-letter
// threshold.
type AuditEventOutboxStore interface {
	EnqueueAuditEvent(ctx context.Context, actor, kind string, subject *string, data []byte, dedupeKey string) (int64, error)
	ClaimAuditEvent(ctx context.Context, consumer string, lease time.Duration) (AuditEventOutbox, error)
	DeliverAuditEvent(ctx context.Context, id int64) (delivered bool, err error)
	FailAuditEvent(ctx context.Context, id int64, cause error) error
	PruneAuditEventOutbox(ctx context.Context, before time.Time) (int64, error)
}

const (
	// AuditEventOutboxMaxAttempts bounds repeated failures. A lease expiry
	// after the last claimed attempt may be reclaimed once more: the queue
	// cannot know whether a process died before or after its transaction, so
	// attempts are a failure-based limit rather than a hard delivery count.
	AuditEventOutboxMaxAttempts = 12

	auditEventOutboxInitialRetryDelay = 5 * time.Second
	auditEventOutboxMaxRetryDelay     = 5 * time.Minute
	auditEventOutboxDefaultLease      = 30 * time.Second

	auditEventOutboxStatePending    = "pending"
	auditEventOutboxStateProcessing = "processing"
	auditEventOutboxStateDelivered  = "delivered"
	auditEventOutboxStateDeadLetter = "dead_letter"
)

func auditEventOutboxLease(lease time.Duration) time.Duration {
	if lease <= 0 {
		return auditEventOutboxDefaultLease
	}
	return lease
}

func auditEventOutboxRetryDelay(attempts int) time.Duration {
	delay := auditEventOutboxInitialRetryDelay
	for i := 1; i < attempts; i++ {
		if delay >= auditEventOutboxMaxRetryDelay/2 {
			return auditEventOutboxMaxRetryDelay
		}
		delay *= 2
	}
	if delay > auditEventOutboxMaxRetryDelay {
		return auditEventOutboxMaxRetryDelay
	}
	return delay
}

func validateAuditEventOutboxInput(actor, kind string, data []byte, dedupeKey string) error {
	if strings.TrimSpace(actor) == "" {
		return errors.New("state: audit event outbox actor required")
	}
	if strings.TrimSpace(kind) == "" {
		return errors.New("state: audit event outbox kind required")
	}
	if strings.TrimSpace(dedupeKey) == "" {
		return errors.New("state: audit event outbox dedupe key required")
	}
	if len(data) == 0 || !json.Valid(data) {
		return errors.New("state: audit event outbox data must be valid JSON")
	}
	return nil
}

func cloneAuditEventOutbox(item AuditEventOutbox) AuditEventOutbox {
	if item.Subject != nil {
		subject := *item.Subject
		item.Subject = &subject
	}
	item.Data = append([]byte(nil), item.Data...)
	return item
}

func auditEventOutboxFailureMessage(cause error) string {
	message := "audit event delivery failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = strings.TrimSpace(cause.Error())
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	return message
}
