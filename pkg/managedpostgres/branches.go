package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261106103654: a branch is a restore, from the latest point apid can
// name or from a chosen one, that may carry a TTL. At its expiry the branch
// sweep revokes its bindings and purges it; the source is never touched.

const (
	// LatestRestoreLag keeps a latest restore's point in the past on the
	// provider's clock as well as apid's.
	LatestRestoreLag = 2 * time.Second
	// MinBranchTTL and MaxBranchTTL bound a restore's TTL.
	MinBranchTTL = api.ManagedPostgresBranchTTLMinSeconds * time.Second
	MaxBranchTTL = api.ManagedPostgresBranchTTLMaxSeconds * time.Second
)

// InBranchAllowance reports a branch that sits in the plan's branch
// allowance instead of its database limit (ADR-20261106103654 amendment 1):
// a customer restore whose TTL is at most
// api.ManagedPostgresAllowanceBranchTTLMax. The SQL counts spell the same
// rule.
func (d Database) InBranchAllowance() bool {
	return d.Preview.IsZero() && d.EnvironmentCloneOperationID == "" && d.RestoreSourceDatabaseID != "" &&
		!d.ExpiresAt.IsZero() && !d.ExpiresAt.After(d.CreatedAt.Add(api.ManagedPostgresAllowanceBranchTTLMax))
}

// countsTowardDatabaseLimit reports a database the plan's database limit
// counts: neither a PR preview database nor a branch in the allowance.
func (d Database) countsTowardDatabaseLimit() bool {
	return d.CountsTowardLimit() && d.Preview.IsZero() && !d.InBranchAllowance()
}

type branchLimitKey struct{}

// reserveAsBranch marks a reservation that takes a place in the branch
// allowance; limit is the owner's allowance.
func reserveAsBranch(ctx context.Context, limit int) context.Context {
	return context.WithValue(ctx, branchLimitKey{}, limit)
}

// branchLimit returns the allowance a reservation was marked with. A branch
// reserved without one is refused: the allowance fails closed.
func branchLimit(ctx context.Context) int {
	limit, _ := ctx.Value(branchLimitKey{}).(int)
	return limit
}

// ErrBranchLimit refuses a short-lived branch past the plan's branch
// allowance. It is an ErrQuotaExceeded.
var ErrBranchLimit error = branchLimitError{}

type branchLimitError struct{}

func (branchLimitError) Error() string {
	return "the account holds as many short-lived branches as its plan includes; delete one, wait for one to expire, or give the new branch a TTL over 7 days so it counts toward the database limit"
}

func (branchLimitError) Unwrap() error { return ErrQuotaExceeded }

// inBranchAllowanceTTL reports a TTL that puts a branch in the allowance.
func inBranchAllowanceTTL(ttl time.Duration) bool {
	return ttl > 0 && ttl <= api.ManagedPostgresAllowanceBranchTTLMax
}

// branchAllowance is the usage subject's branch allowance. Without the hook,
// or for a plan without one, it refuses the branch before anything changes.
func (s *Service) branchAllowance(ctx context.Context, accountID string) (int, error) {
	if s.maxBranchesPerAccount == nil {
		return 0, ErrBranchLimit
	}
	limit, err := s.maxBranchesPerAccount(ctx, PolicySubject(ctx, accountID))
	if err != nil {
		return 0, err
	}
	if limit < 1 {
		return 0, ErrBranchLimit
	}
	if limit > 100 {
		return 0, ErrInvalid
	}
	return limit, nil
}

func validBranchTTL(ttl time.Duration) bool {
	return ttl == 0 || ttl >= MinBranchTTL && ttl <= MaxBranchTTL
}

// validReservationExpiry admits an expiry only on a restore, after its
// creation and within MaxBranchTTL of it.
func validReservationExpiry(database Database) bool {
	if database.ExpiresAt.IsZero() {
		return true
	}
	return database.RestoreSourceDatabaseID != "" && database.ExpiresAt.After(database.CreatedAt) &&
		!database.ExpiresAt.After(database.CreatedAt.Add(MaxBranchTTL))
}

// branchExpiryMatches reports whether an existing copy was made with ttl. The
// catalog keeps microseconds, so the comparison allows less than a
// millisecond of rounding.
func branchExpiryMatches(database Database, ttl time.Duration) bool {
	if ttl == 0 || database.ExpiresAt.IsZero() {
		return ttl == 0 && database.ExpiresAt.IsZero()
	}
	diff := database.ExpiresAt.Sub(database.CreatedAt) - ttl
	return diff > -time.Millisecond && diff < time.Millisecond
}

// BranchExpiryStore is the optional store capability the branch sweep needs.
type BranchExpiryStore interface {
	// ExpiredBranchesPage lists undeleted databases whose expires_at is at
	// or before now, soonest first, after a stable (expires_at, id) cursor.
	// A database already being purged (deleting with no undo deadline) is
	// left out.
	ExpiredBranchesPage(ctx context.Context, now time.Time, after BranchExpiryCursor, limit int) ([]Database, error)
}

type BranchExpiryCursor struct {
	ExpiresAt time.Time
	ID        string
}

// BranchExpirySummary counts one sweep.
type BranchExpirySummary struct {
	Expired int
	Purged  int
	Waiting int
	Failed  int
}

// BranchExpirySweeperOptions configures a BranchExpirySweeper.
type BranchExpirySweeperOptions struct {
	// Purged is called once an expired branch's purge has started (audit
	// event).
	Purged    func(context.Context, Database)
	BatchSize int
	Now       func() time.Time
	Logger    *slog.Logger
}

// BranchExpirySweeper purges branches whose TTL has ended. The lifecycle
// reconciler runs it after each sweep.
type BranchExpirySweeper struct {
	service   *Service
	bindings  RestoreSlotBindings
	purged    func(context.Context, Database)
	batchSize int
	now       func() time.Time
	logger    *slog.Logger
}

func NewBranchExpirySweeper(service *Service, bindings RestoreSlotBindings, options BranchExpirySweeperOptions) (*BranchExpirySweeper, error) {
	if service == nil || bindings == nil {
		return nil, ErrInvalid
	}
	if _, ok := service.store.(BranchExpiryStore); !ok {
		return nil, ErrUnsupported
	}
	if options.BatchSize == 0 {
		options.BatchSize = 50
	}
	if options.BatchSize < 1 || options.BatchSize > 500 {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = service.now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &BranchExpirySweeper{service: service, bindings: bindings, purged: options.Purged,
		batchSize: options.BatchSize, now: options.Now, logger: options.Logger}, nil
}

// Sweep visits each expired branch once. Its bindings are revoked first: a
// database with bindings cannot be deleted. Once none remain, its purge
// starts; a branch that still has copies of its own is held in deleting by
// the purge until they are gone, as for any delete.
func (w *BranchExpirySweeper) Sweep(ctx context.Context) (BranchExpirySummary, error) {
	var summary BranchExpirySummary
	store := w.service.store.(BranchExpiryStore)
	now := w.now()
	var failures []error
	var after BranchExpiryCursor
	for {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		page, err := store.ExpiredBranchesPage(ctx, now, after, w.batchSize)
		if err != nil {
			return summary, errors.Join(append(failures, err)...)
		}
		summary.Expired += len(page)
		for _, database := range page {
			if err := ctx.Err(); err != nil {
				return summary, err
			}
			purged, err := w.visit(ctx, database)
			switch {
			case err != nil:
				summary.Failed++
				failures = append(failures, fmt.Errorf("branch %s: %w", database.ID, err))
			case purged:
				summary.Purged++
			default:
				summary.Waiting++
			}
		}
		if len(page) < w.batchSize {
			break
		}
		last := page[len(page)-1]
		after = BranchExpiryCursor{ExpiresAt: last.ExpiresAt, ID: last.ID}
	}
	return summary, errors.Join(failures...)
}

func (w *BranchExpirySweeper) visit(ctx context.Context, database Database) (bool, error) {
	if database.ExpiresAt.IsZero() || w.now().Before(database.ExpiresAt) || database.RestoreSourceDatabaseID == "" {
		return false, nil
	}
	pending, err := revokeDatabaseBindings(ctx, w.bindings, database)
	if err != nil {
		return false, err
	}
	if pending {
		// The binding reconciler finishes the revocations; the next sweep
		// purges.
		return false, nil
	}
	purged, err := w.service.Delete(ctx, database.AccountID, database.ID)
	if err != nil {
		// A binding still being deleted holds the delete; the next sweep
		// retries.
		if errors.Is(err, ErrConflict) {
			return false, nil
		}
		return false, err
	}
	w.logger.Info("managed postgres branch expired; purge started",
		"database", database.ID, "source", database.RestoreSourceDatabaseID, "expires_at", database.ExpiresAt, "state", purged.State)
	if w.purged != nil {
		w.purged(ctx, purged)
	}
	return true, nil
}
