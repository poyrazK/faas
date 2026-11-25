package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Scale-to-zero suspend timeout: how long a scale-to-zero database's compute
// stays up after its last activity. Every wake pays at least this much
// compute. The platform default is an operator setting (managed_postgres_policy
// suspend_timeout_seconds); a customer may choose a database's own value
// (gregale postgres suspend-timeout), kept apart from the database's spec so
// resize and compute-policy journals never see it. SuspendTimeoutSweeper
// moves each database's compute onto its value.
const (
	DefaultSuspendTimeoutSeconds     int64 = api.ManagedPostgresSuspendTimeoutDefaultSeconds
	MinSuspendTimeoutSeconds         int64 = api.ManagedPostgresSuspendTimeoutPolicyMinSeconds
	MaxSuspendTimeoutSeconds         int64 = api.ManagedPostgresSuspendTimeoutPolicyMaxSeconds
	CustomerMinSuspendTimeoutSeconds int64 = api.ManagedPostgresSuspendTimeoutMinSeconds
	CustomerMaxSuspendTimeoutSeconds int64 = api.ManagedPostgresSuspendTimeoutMaxSeconds
)

// ErrSuspendTimeoutAlwaysOn: an always-on database never suspends, so it has
// no suspend timeout to set.
var ErrSuspendTimeoutAlwaysOn = errors.New("managed postgres: always-on database has no suspend timeout")

func validSuspendTimeout(seconds int64) bool {
	return seconds >= MinSuspendTimeoutSeconds && seconds <= MaxSuspendTimeoutSeconds
}

// ValidCustomerSuspendTimeout reports whether a customer may choose seconds.
func ValidCustomerSuspendTimeout(seconds int64) bool {
	return seconds >= CustomerMinSuspendTimeoutSeconds && seconds <= CustomerMaxSuspendTimeoutSeconds
}

// SuspendTimeoutSetter is a provider whose default scale-to-zero suspend
// timeout the registry sets: at startup and after every applied live policy.
// New databases, restores and compute-policy changes start with it.
type SuspendTimeoutSetter interface {
	SetSuspendTimeout(seconds int64)
}

// SuspendTimeoutResult is what one convergence step did to a database.
type SuspendTimeoutResult int

const (
	// SuspendTimeoutConverged: the database already has the timeout, or is
	// always-on and has none.
	SuspendTimeoutConverged SuspendTimeoutResult = iota
	// SuspendTimeoutChanged: the timeout was written.
	SuspendTimeoutChanged
	// SuspendTimeoutDeferred: the compute is running, held or changing; a
	// later sweep tries again. Only a suspended compute is changed, so the
	// write never touches a compute serving queries.
	SuspendTimeoutDeferred
)

// SuspendTimeoutConverger is a provider that can move an existing database's
// scale-to-zero suspend timeout to seconds.
type SuspendTimeoutConverger interface {
	ConvergeSuspendTimeout(ctx context.Context, providerResourceID string, seconds int64) (SuspendTimeoutResult, error)
}

// SuspendTimeoutStore keeps customers' own suspend timeouts. No entry is the
// platform default.
type SuspendTimeoutStore interface {
	// SetSuspendTimeout stores seconds for the database, or clears its own
	// value when seconds is nil.
	SetSuspendTimeout(ctx context.Context, databaseID string, seconds *int64) error
	SuspendTimeouts(ctx context.Context, databaseIDs []string) (map[string]int64, error)
}

// SuspendTimeoutSeconds is the platform default suspend timeout in force.
func (r *Registry) SuspendTimeoutSeconds() int64 {
	if seconds := r.effective().suspendTimeoutSeconds; seconds != 0 {
		return seconds
	}
	return DefaultSuspendTimeoutSeconds
}

func (r *Registry) pushSuspendTimeout(seconds int64) {
	for _, backend := range r.backends {
		if setter, ok := backend.Provider.(SuspendTimeoutSetter); ok {
			setter.SetSuspendTimeout(seconds)
		}
	}
}

// SuspendTimeout is a database's suspend timeout as the customer sees it.
type SuspendTimeout struct {
	DatabaseID string
	// Seconds is the timeout in force: the database's own or the default.
	Seconds int64
	// Custom is true when the database has its own value.
	Custom bool
	// Applied is true when the compute already runs with Seconds; false
	// means it does the next time it is suspended.
	Applied bool
}

// SuspendTimeouts returns the timeout in force for each scale-to-zero
// database in databases; always-on databases have none.
func (s *Service) SuspendTimeouts(ctx context.Context, databases []Database) (map[string]SuspendTimeout, error) {
	out := make(map[string]SuspendTimeout, len(databases))
	var ids []string
	for _, database := range databases {
		if database.Spec.ScaleToZero {
			ids = append(ids, database.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	custom := map[string]int64{}
	if store, ok := s.store.(SuspendTimeoutStore); ok {
		var err error
		if custom, err = store.SuspendTimeouts(ctx, ids); err != nil {
			return nil, err
		}
	}
	fallback := s.registry.SuspendTimeoutSeconds()
	for _, id := range ids {
		seconds, own := custom[id]
		if !own {
			seconds = fallback
		}
		out[id] = SuspendTimeout{DatabaseID: id, Seconds: seconds, Custom: own}
	}
	return out, nil
}

// SetSuspendTimeout sets a scale-to-zero database's own suspend timeout, or
// with seconds nil returns it to the platform default. The compute takes it
// at once when it is suspended, else the next time it is.
func (s *Service) SetSuspendTimeout(ctx context.Context, accountID, databaseID string, seconds *int64) (SuspendTimeout, error) {
	if accountID == "" || databaseID == "" || seconds != nil && !ValidCustomerSuspendTimeout(*seconds) {
		return SuspendTimeout{}, ErrInvalid
	}
	store, ok := s.store.(SuspendTimeoutStore)
	if !ok {
		return SuspendTimeout{}, ErrUnsupported
	}
	database, err := s.Get(ctx, accountID, databaseID)
	if err != nil {
		return SuspendTimeout{}, err
	}
	if !database.Spec.ScaleToZero {
		return SuspendTimeout{}, ErrSuspendTimeoutAlwaysOn
	}
	if database.State != StateReady {
		// Retried once the database settles.
		return SuspendTimeout{}, ErrConflict
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return SuspendTimeout{}, err
	}
	converger, ok := backend.Provider.(SuspendTimeoutConverger)
	if !ok {
		return SuspendTimeout{}, ErrUnsupported
	}
	current, err := s.SuspendTimeouts(ctx, []Database{database})
	if err != nil {
		return SuspendTimeout{}, err
	}
	target := s.registry.SuspendTimeoutSeconds()
	if seconds != nil {
		target = *seconds
	}
	// A longer timeout keeps the compute up longer: closed operations refuse
	// it and the usage guardrail admits it like any change that can raise
	// spend. A shorter one only cuts spend and is always taken.
	open := s.operationsEnabled()
	if target > current[database.ID].Seconds {
		if !open {
			return SuspendTimeout{}, ErrProvisioningClosed
		}
		if s.admit != nil {
			if err := s.admit(ctx, PolicySubject(ctx, accountID)); err != nil {
				return SuspendTimeout{}, err
			}
		}
	}
	if err := store.SetSuspendTimeout(ctx, database.ID, seconds); err != nil {
		return SuspendTimeout{}, err
	}
	out := SuspendTimeout{DatabaseID: database.ID, Seconds: target, Custom: seconds != nil}
	if !open {
		// No provider write while operations are closed; the sweep moves
		// the compute once they reopen.
		return out, nil
	}
	// Best effort: a running compute, or a provider error, leaves the
	// change to the suspend timeout sweep.
	result, err := converger.ConvergeSuspendTimeout(WithBackgroundRequests(ctx), database.ProviderResourceID, target)
	out.Applied = err == nil && result != SuspendTimeoutDeferred
	return out, nil
}

// ownSuspendTimeout is the database's own suspend timeout for a compute
// change back to scale-to-zero, so the compute comes back on the customer's
// value rather than the default; 0 (the default) for any other change, a
// database without its own value, or a failed read, which the sweep repairs
// once the compute next suspends.
func (s *Service) ownSuspendTimeout(ctx context.Context, databaseID string, request UpdateRequest) int64 {
	if !request.Spec.ScaleToZero || request.PreviousSpec.ScaleToZero {
		return 0
	}
	store, ok := s.store.(SuspendTimeoutStore)
	if !ok {
		return 0
	}
	own, err := store.SuspendTimeouts(ctx, []string{databaseID})
	if err != nil {
		return 0
	}
	return own[databaseID]
}

// SuspendTimeoutLister lists the databases the sweep considers.
type SuspendTimeoutLister interface {
	ListUsageDatabases(ctx context.Context, after UsageDatabaseCursor, limit int) ([]Database, error)
}

// SuspendTimeoutSweepSummary counts one sweep's databases.
type SuspendTimeoutSweepSummary struct {
	DefaultSeconds int64
	// Unchanged counts databases skipped because the sweep already put
	// their current row on their current value.
	Unchanged int
	Converged int
	Changed   int
	Deferred  int
	Failed    int
}

// suspendTimeoutMemo is what the sweep last confirmed for one database: the
// value, on the database row as it was then. A compute-policy change, a
// restore or a cutover moves the row's UpdatedAt, a customer or operator
// change moves the value, and either sends the database through again.
type suspendTimeoutMemo struct {
	target    int64
	resource  string
	updatedAt time.Time
}

// SuspendTimeoutSweeper moves existing scale-to-zero databases onto their
// suspend timeout: their own, else the platform default. It reads the
// provider only for databases whose row or value changed since it last
// confirmed them, or that it had to defer.
type SuspendTimeoutSweeper struct {
	registry *Registry
	store    SuspendTimeoutLister
	logger   *slog.Logger
	// maxChanges bounds the writes of one sweep; the rest are deferred.
	maxChanges int
	mu         sync.Mutex
	confirmed  map[string]suspendTimeoutMemo
}

const (
	suspendTimeoutSweepBatch = 100
	// SuspendTimeoutSweepInterval is how often apid sweeps: a value set
	// while a database runs lands within about this long of its suspend.
	SuspendTimeoutSweepInterval = 5 * time.Minute
)

func NewSuspendTimeoutSweeper(registry *Registry, store SuspendTimeoutLister, logger *slog.Logger) (*SuspendTimeoutSweeper, error) {
	if registry == nil || store == nil {
		return nil, ErrInvalid
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SuspendTimeoutSweeper{registry: registry, store: store, logger: logger, maxChanges: 100,
		confirmed: map[string]suspendTimeoutMemo{}}, nil
}

// RunSuspendTimeoutSweeper sweeps the service's databases every
// SuspendTimeoutSweepInterval until ctx ends; nil when the store cannot list
// them. Every apid replica runs it: the writes are idempotent and touch only
// suspended computes.
func (s *Service) RunSuspendTimeoutSweeper(ctx context.Context, logger *slog.Logger) error {
	lister, ok := s.store.(SuspendTimeoutLister)
	if !ok {
		return nil
	}
	sweeper, err := NewSuspendTimeoutSweeper(s.registry, lister, logger)
	if err != nil {
		return err
	}
	return sweeper.Run(ctx, SuspendTimeoutSweepInterval)
}

// Run sweeps every interval until ctx ends. It runs whether or not usage
// metering is on.
func (s *SuspendTimeoutSweeper) Run(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if summary, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			s.logger.Warn("managed postgres suspend timeout sweep failed", "error", err, "changed", summary.Changed, "failed", summary.Failed)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Sweep runs one pass over ready scale-to-zero databases.
func (s *SuspendTimeoutSweeper) Sweep(ctx context.Context) (SuspendTimeoutSweepSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx = WithBackgroundRequests(ctx)
	fallback := s.registry.SuspendTimeoutSeconds()
	summary := SuspendTimeoutSweepSummary{DefaultSeconds: fallback}
	customStore, _ := s.store.(SuspendTimeoutStore)
	seen := make(map[string]bool, len(s.confirmed))
	var failures []error
	var after UsageDatabaseCursor
	for {
		databases, err := s.store.ListUsageDatabases(ctx, after, suspendTimeoutSweepBatch)
		if err != nil {
			return summary, err
		}
		var candidates []Database
		var ids []string
		for _, database := range databases {
			if database.State == StateReady && database.DeletedAt == nil && database.ProviderResourceID != "" && database.Spec.ScaleToZero {
				candidates = append(candidates, database)
				ids = append(ids, database.ID)
			}
		}
		custom := map[string]int64{}
		if customStore != nil && len(ids) > 0 {
			if custom, err = customStore.SuspendTimeouts(ctx, ids); err != nil {
				return summary, err
			}
		}
		for _, database := range candidates {
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			seen[database.ID] = true
			target, own := custom[database.ID]
			if !own {
				target = fallback
			}
			memo := suspendTimeoutMemo{target: target, resource: database.ProviderResourceID, updatedAt: database.UpdatedAt}
			if s.confirmed[database.ID] == memo {
				summary.Unchanged++
				continue
			}
			s.sweepOne(ctx, database, target, customStore, &summary, &failures)
		}
		if len(databases) < suspendTimeoutSweepBatch {
			break
		}
		last := databases[len(databases)-1]
		after = UsageDatabaseCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	for id := range s.confirmed {
		if !seen[id] {
			delete(s.confirmed, id)
		}
	}
	return summary, errors.Join(failures...)
}

func (s *SuspendTimeoutSweeper) sweepOne(ctx context.Context, database Database, target int64, customStore SuspendTimeoutStore, summary *SuspendTimeoutSweepSummary, failures *[]error) {
	fail := func(err error) {
		summary.Failed++
		*failures = append(*failures, fmt.Errorf("database %s: %w", database.ID, err))
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		fail(err)
		return
	}
	converger, ok := backend.Provider.(SuspendTimeoutConverger)
	if !ok {
		return
	}
	if summary.Changed >= s.maxChanges {
		summary.Deferred++
		return
	}
	// Read the database's own value again just before writing: a customer
	// change since the batch read must not be overwritten with the old one.
	if customStore != nil {
		current, err := customStore.SuspendTimeouts(ctx, []string{database.ID})
		if err != nil {
			fail(err)
			return
		}
		if own, ok := current[database.ID]; ok {
			target = own
		} else {
			target = s.registry.SuspendTimeoutSeconds()
		}
	}
	result, err := converger.ConvergeSuspendTimeout(ctx, database.ProviderResourceID, target)
	switch {
	case err != nil:
		fail(err)
		return
	case result == SuspendTimeoutChanged:
		summary.Changed++
		s.logger.Info("managed postgres suspend timeout converged", "database_id", database.ID, "suspend_timeout_seconds", target)
	case result == SuspendTimeoutDeferred:
		summary.Deferred++
		return
	default:
		summary.Converged++
	}
	s.confirmed[database.ID] = suspendTimeoutMemo{target: target, resource: database.ProviderResourceID, updatedAt: database.UpdatedAt}
}
