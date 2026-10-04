package commit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Receipt struct {
	ID           string `json:"receipt_id"`
	InvocationID string `json:"invocation_id,omitempty"`
	OperationID  string `json:"operation_id,omitempty"`
}

// Acceptor must durably deduplicate source/event identity and return the
// original receipt on replay. A successful response means accepted, not done.
type Acceptor interface {
	Accept(context.Context, Event) (Receipt, error)
}

// PermanentError blocks an event until explicitly repaired/replayed. It must
// contain only a stable public code, never credentials or database error text.
type PermanentError struct{ Code string }

func (e *PermanentError) Error() string { return e.Code }

type Relay struct {
	SourceID   string
	Pool       *pgxpool.Pool
	Acceptor   Acceptor
	Batch      int
	Lease      time.Duration
	RetryDelay time.Duration
}

// Tick leases committed rows in a short transaction, then performs remote work
// outside the customer transaction. Lease fencing prevents stale workers from
// overwriting a newer worker's checkpoint. An uncertain acceptance is retried.
func (r *Relay) Tick(ctx context.Context) (int, error) {
	if r.Pool == nil || r.Acceptor == nil {
		return 0, errors.New("commit: relay dependencies missing")
	}
	batch := r.Batch
	if batch == 0 {
		batch = 32
	}
	if batch < 1 || batch > 128 {
		return 0, errors.New("commit: batch must be 1-128")
	}
	lease := r.Lease
	if lease == 0 {
		lease = time.Minute
	}
	if lease < time.Second || lease > 5*time.Minute {
		return 0, errors.New("commit: lease must be 1s-5m")
	}
	delay := r.RetryDelay
	if delay == 0 {
		delay = 5 * time.Second
	}
	if delay < time.Second {
		return 0, errors.New("commit: retry delay must be at least 1s")
	}
	var source any
	if r.SourceID != "" {
		if err := QualifySource(ctx, r.Pool, r.SourceID); err != nil {
			return 0, err
		}
		source = r.SourceID
	}
	token := uuid.NewString()
	rows, err := r.Pool.Query(ctx, `WITH candidates AS (
 SELECT event_id FROM public.gregale_outbox
 WHERE accepted_at IS NULL AND blocked_code IS NULL
 AND ($4::uuid IS NULL OR EXISTS(SELECT 1 FROM public.gregale_commit_binding WHERE singleton AND source_id=$4::uuid))
 AND next_attempt_at <= clock_timestamp()
 AND (lease_until IS NULL OR lease_until <= clock_timestamp())
 ORDER BY next_attempt_at,created_at,event_id FOR UPDATE SKIP LOCKED LIMIT $1
 ) UPDATE public.gregale_outbox o SET lease_token=$2::uuid,
 lease_until=clock_timestamp()+($3::bigint * interval '1 millisecond'), attempts=attempts+1
 FROM candidates c WHERE o.event_id=c.event_id
 RETURNING o.event_id::text,o.event_type,o.payload`, batch, token, lease.Milliseconds(), source)
	if err != nil {
		return 0, fmt.Errorf("commit: claim events: %w", err)
	}
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Type, &e.Data); err != nil {
			rows.Close()
			return 0, err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	accepted := 0
	var failures []error
	for _, e := range events {
		receipt, acceptErr := r.Acceptor.Accept(ctx, e)
		if acceptErr == nil {
			if _, err := uuid.Parse(receipt.ID); err != nil {
				acceptErr = errors.New("commit: invalid acceptance receipt")
			}
			workID := receipt.OperationID
			if workID == "" {
				workID = receipt.InvocationID
			}
			if _, err := uuid.Parse(workID); err != nil || (receipt.OperationID != "" && receipt.InvocationID != "") {
				acceptErr = errors.New("commit: invalid acceptance work identity")
			}
		}
		if acceptErr != nil {
			var permanent *PermanentError
			var code any
			if errors.As(acceptErr, &permanent) && permanent.Code != "" {
				code = permanent.Code
			}
			_, err := r.Pool.Exec(ctx, `UPDATE public.gregale_outbox SET lease_token=NULL,lease_until=NULL,
    next_attempt_at=clock_timestamp()+($3::bigint * interval '1 millisecond'),blocked_code=$4
    WHERE event_id=$1::uuid AND lease_token=$2::uuid AND accepted_at IS NULL`, e.ID, token, delay.Milliseconds(), code)
			failures = append(failures, acceptErr)
			if err != nil {
				failures = append(failures, err)
			}
			continue
		}
		result, err := r.Pool.Exec(ctx, `UPDATE public.gregale_outbox SET accepted_at=clock_timestamp(),
   receipt_id=$3::uuid,invocation_id=NULLIF($4,'')::uuid,operation_id=NULLIF($5,'')::uuid,lease_token=NULL,lease_until=NULL
   WHERE event_id=$1::uuid AND lease_token=$2::uuid AND accepted_at IS NULL`, e.ID, token, receipt.ID, receipt.InvocationID, receipt.OperationID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		accepted += int(result.RowsAffected())
	}
	return accepted, errors.Join(failures...)
}

// Cleanup removes only accepted rows older than the owner's retention window.
// Acceptance identities must remain durable in Gregale after source cleanup.
func (r *Relay) Cleanup(ctx context.Context, before time.Time, limit int) (int64, error) {
	if r.Pool == nil || limit < 1 || limit > 128 {
		return 0, errors.New("commit: invalid cleanup configuration")
	}
	tag, err := r.Pool.Exec(ctx, `DELETE FROM public.gregale_outbox WHERE event_id IN (
  SELECT event_id FROM public.gregale_outbox WHERE accepted_at IS NOT NULL AND accepted_at<$1
  ORDER BY accepted_at,event_id FOR UPDATE SKIP LOCKED LIMIT $2)`, before, limit)
	return tag.RowsAffected(), err
}
