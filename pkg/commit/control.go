package commit

import (
	"context"
	"errors"
	"time"
)

type Backlog struct {
	Pending       int64      `json:"pending"`
	Blocked       int64      `json:"blocked"`
	Accepted      int64      `json:"accepted"`
	OldestPending *time.Time `json:"oldest_pending,omitempty"`
}

var ErrReplayLeaseBusy = errors.New("commit: blocked event still has a live lease")

func (r *Relay) Backlog(ctx context.Context) (Backlog, error) {
	var b Backlog
	if r.Pool == nil {
		return b, errors.New("commit: customer database unavailable")
	}
	err := r.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE accepted_at IS NULL AND blocked_code IS NULL),
 count(*) FILTER(WHERE accepted_at IS NULL AND blocked_code IS NOT NULL),
 count(*) FILTER(WHERE accepted_at IS NOT NULL),min(created_at) FILTER(WHERE accepted_at IS NULL)
 FROM public.gregale_outbox`).Scan(&b.Pending, &b.Blocked, &b.Accepted, &b.OldestPending)
	return b, err
}

// ReplayBlocked preserves the event ID and payload. A changed accepted event
// cannot be republished under the old identity by clearing a block.
func (r *Relay) ReplayBlocked(ctx context.Context, eventID string) (bool, error) {
	if r.Pool == nil {
		return false, errors.New("commit: customer database unavailable")
	}
	tag, err := r.Pool.Exec(ctx, `UPDATE public.gregale_outbox SET blocked_code=NULL,next_attempt_at=clock_timestamp(),
 lease_token=NULL,lease_until=NULL WHERE event_id=$1::uuid AND accepted_at IS NULL AND blocked_code IS NOT NULL
	AND (lease_until IS NULL OR lease_until<=clock_timestamp())`, eventID)
	if err == nil && tag.RowsAffected() == 0 {
		var busy bool
		err = r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.gregale_outbox WHERE event_id=$1::uuid AND accepted_at IS NULL AND blocked_code IS NOT NULL AND lease_until>clock_timestamp())`, eventID).Scan(&busy)
		if err == nil && busy {
			return false, ErrReplayLeaseBusy
		}
	}
	return tag.RowsAffected() == 1, err
}

// Run is the managed polling lifecycle. A source outage never exits the loop;
// cancellation ends it. Reporting receives only a boolean health signal, so
// secrets in driver errors cannot escape through ordinary platform telemetry.
func (r *Relay) Run(ctx context.Context, interval time.Duration, report func(int, bool)) error {
	if interval < time.Second || interval > time.Minute {
		return errors.New("commit: poll interval must be 1s-1m")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tickCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		n, err := r.Tick(tickCtx)
		cancel()
		if report != nil {
			report(n, err == nil)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
