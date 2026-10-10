package commit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

type Receipt struct {
	Publication  *api.CommitEventPublication `json:"publication,omitempty"`
	ID           string                      `json:"receipt_id"`
	InvocationID string                      `json:"invocation_id,omitempty"`
	OperationID  string                      `json:"operation_id,omitempty"`
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
	ContractVersion int
	SourceID        string
	Pool            *pgxpool.Pool
	Acceptor        Acceptor
	Batch           int
	Lease           time.Duration
	RetryDelay      time.Duration
}

// Tick leases committed rows in a short transaction, then performs remote work
// outside the customer transaction. Lease fencing prevents stale workers from
// overwriting a newer worker's checkpoint. An uncertain acceptance is retried.
func (r *Relay) Tick(ctx context.Context) (int, error) {
	if r.Pool == nil || r.Acceptor == nil || (r.ContractVersion == 3 && r.SourceID == "") {
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
	RETURNING o.event_id::text,o.event_type,o.payload,to_jsonb(o)->'routing',COALESCE(to_jsonb(o)->>'schema_version','')`, batch, token, lease.Milliseconds(), source)
	if err != nil {
		return 0, fmt.Errorf("commit: claim events: %w", err)
	}
	type claimedEvent struct {
		event          Event
		invalidRouting bool
	}
	var events []claimedEvent
	for rows.Next() {
		var e Event
		var routing json.RawMessage
		if err := rows.Scan(&e.ID, &e.Type, &e.Data, &routing, &e.SchemaVersion); err != nil {
			rows.Close()
			return 0, err
		}
		invalid := false
		if len(routing) > 0 && string(routing) != "null" {
			decoder := json.NewDecoder(bytes.NewReader(routing))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&e.Routing); err != nil {
				invalid = true
			}
			if _, err := NormalizeRouting(e.Routing); err != nil {
				invalid = true
			}
		}
		events = append(events, claimedEvent{event: e, invalidRouting: invalid})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	accepted := 0
	var failures []error
	for _, claimed := range events {
		e := claimed.event
		var receipt Receipt
		var acceptErr error
		if claimed.invalidRouting {
			acceptErr = &PermanentError{Code: "invalid_routing"}
		} else {
			receipt, acceptErr = r.Acceptor.Accept(ctx, e)
		}
		if acceptErr == nil {
			if _, err := uuid.Parse(receipt.ID); err != nil {
				acceptErr = errors.New("commit: invalid acceptance receipt")
			}
			if receipt.Publication != nil {
				pub := receipt.Publication
				if r.ContractVersion != 3 || receipt.OperationID != "" || receipt.InvocationID != "" || pub.AcceptanceID <= 0 || !r.publicationMatches(e, pub) || pub.AcceptedAt.IsZero() || pub.ReceiptURL == "" {
					acceptErr = errors.New("commit: invalid publication identity")
				}
			} else {
				workID := receipt.OperationID
				if workID == "" {
					workID = receipt.InvocationID
				}
				if _, err := uuid.Parse(workID); err != nil || (receipt.OperationID != "" && receipt.InvocationID != "") || r.ContractVersion == 3 {
					acceptErr = errors.New("commit: invalid acceptance work identity")
				}
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
		var resultRows int64
		var checkpointErr error
		if receipt.Publication != nil {
			tag, err := r.Pool.Exec(ctx, `UPDATE public.gregale_outbox SET accepted_at=clock_timestamp(),receipt_id=$3::uuid,
    publication_acceptance_id=$4,invocation_id=NULL,operation_id=NULL,lease_token=NULL,lease_until=NULL
    WHERE event_id=$1::uuid AND lease_token=$2::uuid AND accepted_at IS NULL`, e.ID, token, receipt.ID, receipt.Publication.AcceptanceID)
			resultRows, checkpointErr = tag.RowsAffected(), err
		} else {
			tag, err := r.Pool.Exec(ctx, `UPDATE public.gregale_outbox SET accepted_at=clock_timestamp(),
    receipt_id=$3::uuid,invocation_id=NULLIF($4,'')::uuid,operation_id=NULLIF($5,'')::uuid,lease_token=NULL,lease_until=NULL
    WHERE event_id=$1::uuid AND lease_token=$2::uuid AND accepted_at IS NULL`, e.ID, token, receipt.ID, receipt.InvocationID, receipt.OperationID)
			resultRows, checkpointErr = tag.RowsAffected(), err
		}
		err = checkpointErr
		if err != nil {
			failures = append(failures, err)
			continue
		}
		accepted += int(resultRows)
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

// A publication checkpoint must identify this source's exact outbox event, not
// merely contain a syntactically valid acceptance from a different publication.
func (r *Relay) publicationMatches(event Event, publication *api.CommitEventPublication) bool {
	source, err := uuid.Parse(r.SourceID)
	if err != nil {
		return false
	}
	if publication.ID != "commit."+source.String()+"."+event.ID {
		return false
	}
	if !strings.HasPrefix(publication.Source, "app.") {
		return false
	}
	app, err := uuid.Parse(strings.TrimPrefix(publication.Source, "app."))
	return err == nil && app != uuid.Nil && publication.Source == "app."+app.String()
}
