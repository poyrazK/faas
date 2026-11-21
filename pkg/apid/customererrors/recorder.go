// Package customererrors records the control-plane API problems apid returns
// to authenticated customers, so operators can see what a customer was
// refused (ADR-20261112170000).
//
// Recording never blocks or fails a request: Record validates and bounds the
// row, then hands it to a buffered channel that Run drains in batches. When
// the channel is full or an account exceeds its per-minute budget, the row is
// dropped and counted. Only identifiers are stored: the matched route
// pattern, the problem code, the app slug, and the request and trace ids.
// Bodies, query strings and problem details never reach this package.
package customererrors

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// Recording bounds.
const (
	// QueueSize is the number of rows waiting for a flush. A full queue
	// drops new rows rather than delaying the request.
	QueueSize = 4096
	// AccountBudgetPerMinute caps rows per account per minute on one
	// replica, so one misbehaving client cannot fill the table.
	AccountBudgetPerMinute = 60
	// BatchSize and FlushInterval bound how long a row waits in memory.
	BatchSize     = 256
	FlushInterval = time.Second
	// PruneInterval and pruneBatch bound retention deletes.
	PruneInterval = time.Hour
	pruneBatch    = 5000
	maxRouteBytes = 200
)

var (
	codePattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,79}$`)
	slugPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	traceIDPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	uuidPattern      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Event is one refused request, before validation.
type Event struct {
	AccountID  string
	OccurredAt time.Time
	Route      string
	Code       string
	AppSlug    string
	RequestID  string
	TraceID    string
}

// Recorder buffers and persists Events. A nil *Recorder records nothing and
// reads nothing, which is how apid runs without PostgreSQL.
type Recorder struct {
	pool    *pgxpool.Pool
	queue   chan Event
	now     func() time.Time
	log     *slog.Logger
	dropped atomic.Int64

	mu      sync.Mutex
	minute  int64
	budgets map[string]int
}

// New returns a Recorder writing to pool, or nil when pool is nil.
func New(pool *pgxpool.Pool, log *slog.Logger) *Recorder {
	if pool == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Recorder{pool: pool, queue: make(chan Event, QueueSize), now: time.Now, log: log, budgets: map[string]int{}}
}

// Dropped is the number of events this replica did not record.
func (r *Recorder) Dropped() int64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// ValidCode reports whether code is a well-formed problem code, the same
// check Normalize applies; metrics use it to bound their label values.
func ValidCode(code string) bool { return codePattern.MatchString(code) }

// Normalize validates and bounds an Event. It reports false when the event
// cannot be recorded (no account, route or valid code). Optional fields that
// fail validation are cleared rather than rejected.
func Normalize(e Event) (Event, bool) {
	if !uuidPattern.MatchString(e.AccountID) || e.Route == "" || !codePattern.MatchString(e.Code) {
		return Event{}, false
	}
	if len(e.Route) > maxRouteBytes {
		e.Route = e.Route[:maxRouteBytes]
	}
	if !slugPattern.MatchString(e.AppSlug) {
		e.AppSlug = ""
	}
	if !requestIDPattern.MatchString(e.RequestID) {
		e.RequestID = ""
	}
	if !traceIDPattern.MatchString(e.TraceID) || e.TraceID == "00000000000000000000000000000000" {
		e.TraceID = ""
	}
	e.OccurredAt = e.OccurredAt.UTC()
	return e, true
}

// Record queues e without blocking.
func (r *Recorder) Record(e Event) {
	if r == nil {
		return
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = r.now()
	}
	e, ok := Normalize(e)
	if !ok {
		return
	}
	if !r.admit(e.AccountID, e.OccurredAt) {
		r.dropped.Add(1)
		return
	}
	select {
	case r.queue <- e:
	default:
		r.dropped.Add(1)
	}
}

// admit spends one unit of the account's budget for the current minute.
func (r *Recorder) admit(accountID string, at time.Time) bool {
	minute := at.Unix() / 60
	r.mu.Lock()
	defer r.mu.Unlock()
	if minute != r.minute {
		r.minute = minute
		clear(r.budgets)
	}
	if r.budgets[accountID] >= AccountBudgetPerMinute {
		return false
	}
	r.budgets[accountID]++
	return true
}

// Run flushes queued events and prunes expired rows until ctx ends.
func (r *Recorder) Run(ctx context.Context) {
	if r == nil {
		return
	}
	flush := time.NewTicker(FlushInterval)
	defer flush.Stop()
	prune := time.NewTicker(PruneInterval)
	defer prune.Stop()
	batch := make([]Event, 0, BatchSize)
	write := func() {
		if len(batch) == 0 {
			return
		}
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := r.insert(writeCtx, batch); err != nil {
			r.dropped.Add(int64(len(batch)))
			r.log.Warn("customer api errors: insert failed", "rows", len(batch), "err", err)
		}
		cancel()
		batch = batch[:0]
	}
	r.pruneExpired(ctx)
	for {
		select {
		case <-ctx.Done():
			// Drain what is already queued, then stop.
			for {
				select {
				case e := <-r.queue:
					batch = append(batch, e)
					if len(batch) == BatchSize {
						write()
					}
				default:
					write()
					return
				}
			}
		case e := <-r.queue:
			batch = append(batch, e)
			if len(batch) == BatchSize {
				write()
			}
		case <-flush.C:
			write()
		case <-prune.C:
			r.pruneExpired(ctx)
		}
	}
}

func (r *Recorder) insert(ctx context.Context, batch []Event) error {
	_, err := r.pool.CopyFrom(ctx, pgx.Identifier{"customer_api_errors"},
		[]string{"account_id", "occurred_at", "route", "code", "app_slug", "request_id", "trace_id"},
		pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
			e := batch[i]
			return []any{e.AccountID, e.OccurredAt, e.Route, e.Code, e.AppSlug, e.RequestID, e.TraceID}, nil
		}))
	return err
}

// PruneExpired deletes rows older than api.CustomerErrorRetention in bounded
// batches and returns how many it deleted.
func (r *Recorder) PruneExpired(ctx context.Context) (int64, error) {
	if r == nil {
		return 0, nil
	}
	cutoff := r.now().Add(-api.CustomerErrorRetention)
	var total int64
	for {
		tag, err := r.pool.Exec(ctx, `DELETE FROM customer_api_errors WHERE id IN (
			SELECT id FROM customer_api_errors WHERE occurred_at < $1 ORDER BY occurred_at LIMIT $2)`, cutoff, pruneBatch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < pruneBatch {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

func (r *Recorder) pruneExpired(ctx context.Context) {
	if _, err := r.PruneExpired(ctx); err != nil && ctx.Err() == nil {
		r.log.Warn("customer api errors: prune failed", "err", err)
	}
}

// Summary returns the account's errors since since: counts by code and the
// most recent rows.
func (r *Recorder) Summary(ctx context.Context, accountID string, since time.Time) (api.ObsCustomerErrorSummary, error) {
	out := api.ObsCustomerErrorSummary{
		WindowHours: int(api.CustomerErrorWindow / time.Hour),
		ByCode:      []api.ObsCustomerErrorCode{},
		Recent:      []api.ObsCustomerErrorEvent{},
	}
	if r == nil {
		return out, nil
	}
	out.Dropped = r.Dropped()
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM customer_api_errors WHERE account_id=$1 AND occurred_at>=$2`,
		accountID, since).Scan(&out.Total); err != nil {
		return out, err
	}
	if out.Total == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT code, count(*), max(occurred_at) FROM customer_api_errors
		WHERE account_id=$1 AND occurred_at>=$2 GROUP BY code ORDER BY count(*) DESC, code LIMIT $3`,
		accountID, since, api.CustomerErrorCodeLimit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c api.ObsCustomerErrorCode
		if err := rows.Scan(&c.Code, &c.Count, &c.LastAt); err != nil {
			rows.Close()
			return out, err
		}
		c.LastAt = c.LastAt.UTC()
		out.ByCode = append(out.ByCode, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = r.pool.Query(ctx, `SELECT occurred_at, route, code, app_slug, request_id, trace_id FROM customer_api_errors
		WHERE account_id=$1 AND occurred_at>=$2 ORDER BY occurred_at DESC, id DESC LIMIT $3`,
		accountID, since, api.CustomerErrorRecentLimit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e api.ObsCustomerErrorEvent
		if err := rows.Scan(&e.OccurredAt, &e.Route, &e.Code, &e.AppSlug, &e.RequestID, &e.TraceID); err != nil {
			return out, err
		}
		e.OccurredAt = e.OccurredAt.UTC()
		out.Recent = append(out.Recent, e)
	}
	return out, rows.Err()
}

// RequestForAccount returns the newest recorded error of accountID whose
// request id or trace id equals id, or nil. Unlike Lookup it never returns
// another account's row, so customer-supplied ids are safe to resolve.
func (r *Recorder) RequestForAccount(ctx context.Context, accountID, id string) (*api.AccountHealthRequest, error) {
	if r == nil || !uuidPattern.MatchString(accountID) || !requestIDPattern.MatchString(id) {
		return nil, nil
	}
	var e api.AccountHealthRequest
	err := r.pool.QueryRow(ctx, `SELECT occurred_at, route, code, app_slug, request_id, trace_id
		FROM customer_api_errors WHERE account_id=$1 AND (request_id=$2 OR trace_id=$2)
		ORDER BY occurred_at DESC, id DESC LIMIT 1`, accountID, id).
		Scan(&e.OccurredAt, &e.Route, &e.Code, &e.AppSlug, &e.RequestID, &e.TraceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.OccurredAt = e.OccurredAt.UTC()
	return &e, nil
}

// Lookup finds recorded errors whose request id or trace id equals id.
func (r *Recorder) Lookup(ctx context.Context, id string) ([]api.ObsLookupMatch, error) {
	if r == nil || !requestIDPattern.MatchString(id) {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT account_id::text, app_slug, code, occurred_at, trace_id=$1
		FROM customer_api_errors WHERE request_id=$1 OR trace_id=$1
		ORDER BY occurred_at DESC, id DESC LIMIT $2`, id, api.ObsLookupMaxMatches)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []api.ObsLookupMatch
	for rows.Next() {
		var (
			m       api.ObsLookupMatch
			code    string
			at      time.Time
			isTrace bool
		)
		if err := rows.Scan(&m.AccountID, &m.AppSlug, &code, &at, &isTrace); err != nil {
			return nil, err
		}
		at = at.UTC()
		m.Kind, m.Detail, m.At = api.ObsLookupRequest, "API error "+code, &at
		if isTrace {
			m.Kind = api.ObsLookupTrace
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
