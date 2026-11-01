package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261031123259: a point-in-time restore may reserve one database past
// the owner's database limit. The copy holds the restore slot until the owner
// has room again, when it becomes an ordinary database, or until its window
// ends without room, when its delete is scheduled like a customer's.

// RestoreSlotStore is the optional store capability the slot sweep needs.
type RestoreSlotStore interface {
	// RestoreSlots lists undeleted databases holding the restore slot,
	// soonest end first, with the usage subject whose limit they are over.
	RestoreSlots(ctx context.Context, limit int) ([]RestoreSlotHolder, error)
	// CountSubjectDatabases counts what a reservation for subjectID counts:
	// its undeleted databases and pending clone restores.
	CountSubjectDatabases(ctx context.Context, subjectID string) (int64, error)
	// ClearRestoreSlot makes a slot holder an ordinary database. It reports
	// whether the database still held the slot.
	ClearRestoreSlot(ctx context.Context, databaseID string, now time.Time) (bool, error)
}

// RestoreSlotHolder is a database holding the restore slot.
type RestoreSlotHolder struct {
	Database  Database
	SubjectID string
}

// RestoreSlotBindings revokes an expired copy's bindings before its delete is
// scheduled: a database with bindings cannot be deleted. BindingService
// implements it.
type RestoreSlotBindings interface {
	List(ctx context.Context, accountID, databaseID string) ([]Binding, error)
	Delete(ctx context.Context, accountID, bindingID string) (Binding, error)
}

// RestoreSlotSummary counts one sweep.
type RestoreSlotSummary struct {
	Holders  int
	Released int
	Expired  int
	Waiting  int
	Failed   int
	// Noticed counts notices recorded; NotifyFailed notices whose email
	// was not queued (the next sweep retries them).
	Noticed      int
	NotifyFailed int
}

// RestoreSlotSweeperOptions configures a RestoreSlotSweeper.
type RestoreSlotSweeperOptions struct {
	// Expired is called once a copy's delete is scheduled because its slot
	// ended without room (audit event).
	Expired func(context.Context, Database)
	// Notify queues the email for a recorded notice. It is retried each
	// sweep until it links an email to the notice or the notice is a day
	// old, so it must mail a notice at most once.
	Notify    func(context.Context, RestoreSlotNotice) error
	BatchSize int
	Now       func() time.Time
	Logger    *slog.Logger
}

// RestoreSlotSweeper releases and ends restore slots. The lifecycle
// reconciler runs it after each sweep.
type RestoreSlotSweeper struct {
	service   *Service
	bindings  RestoreSlotBindings
	expired   func(context.Context, Database)
	notify    func(context.Context, RestoreSlotNotice) error
	batchSize int
	now       func() time.Time
	logger    *slog.Logger
}

func NewRestoreSlotSweeper(service *Service, bindings RestoreSlotBindings, options RestoreSlotSweeperOptions) (*RestoreSlotSweeper, error) {
	if service == nil || bindings == nil {
		return nil, ErrInvalid
	}
	if _, ok := service.store.(RestoreSlotStore); !ok {
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
	return &RestoreSlotSweeper{service: service, bindings: bindings, expired: options.Expired, notify: options.Notify,
		batchSize: options.BatchSize, now: options.Now, logger: options.Logger}, nil
}

// Sweep visits each slot holder once. A holder whose owner has room again is
// released. One whose window has ended without room has its bindings revoked
// and, once none remain, its delete scheduled; the source is never touched.
func (w *RestoreSlotSweeper) Sweep(ctx context.Context) (RestoreSlotSummary, error) {
	var summary RestoreSlotSummary
	store := w.service.store.(RestoreSlotStore)
	holders, err := store.RestoreSlots(ctx, w.batchSize)
	if err != nil {
		return summary, err
	}
	summary.Holders = len(holders)
	var failures []error
	for _, holder := range holders {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		outcome, err := w.visit(ctx, store, holder, &summary)
		switch {
		case err != nil:
			summary.Failed++
			failures = append(failures, fmt.Errorf("restore slot %s: %w", holder.Database.ID, err))
		case outcome == restoreSlotReleased:
			summary.Released++
		case outcome == restoreSlotExpired:
			summary.Expired++
		case outcome == restoreSlotWaiting:
			summary.Waiting++
		}
	}
	w.mailNotices(ctx, &summary)
	return summary, errors.Join(failures...)
}

type restoreSlotOutcome int

const (
	restoreSlotHeld restoreSlotOutcome = iota
	restoreSlotReleased
	restoreSlotExpired
	restoreSlotWaiting
)

func (w *RestoreSlotSweeper) visit(ctx context.Context, store RestoreSlotStore, holder RestoreSlotHolder, summary *RestoreSlotSummary) (restoreSlotOutcome, error) {
	database := holder.Database
	room, err := w.service.restoreSlotRoom(ctx, store, holder.SubjectID)
	if err != nil {
		return restoreSlotHeld, err
	}
	now := w.now()
	if room {
		if _, err := store.ClearRestoreSlot(ctx, database.ID, now); err != nil {
			return restoreSlotHeld, err
		}
		return restoreSlotReleased, nil
	}
	// A delete already scheduled, by the customer or an earlier sweep, keeps
	// the slot until the copy is purged.
	if database.State == StateDeleting {
		return restoreSlotHeld, nil
	}
	if now.Before(database.RestoreSlotUntil) {
		if !now.Before(database.RestoreSlotUntil.Add(-api.ManagedPostgresRestoreSlotNoticeLead)) {
			w.recordNotice(ctx, RestoreSlotNotice{DatabaseID: database.ID, Kind: RestoreSlotNoticeEnding,
				SlotUntil: database.RestoreSlotUntil, RecordedAt: now}, summary)
		}
		return restoreSlotHeld, nil
	}
	pending, err := w.revokeBindings(ctx, database)
	if err != nil || pending {
		return restoreSlotWaiting, err
	}
	scheduled, err := w.service.ScheduleDelete(ctx, database.AccountID, database.ID)
	if err != nil {
		// A binding still being deleted holds the delete; the next sweep
		// retries.
		if errors.Is(err, ErrConflict) {
			return restoreSlotWaiting, nil
		}
		return restoreSlotHeld, err
	}
	w.logger.Info("managed postgres restore slot ended without room; delete scheduled",
		"database", database.ID, "subject_id", holder.SubjectID, "restorable_until", scheduled.PurgeAfter)
	if w.expired != nil {
		w.expired(ctx, scheduled)
	}
	w.recordNotice(ctx, RestoreSlotNotice{DatabaseID: database.ID, Kind: RestoreSlotNoticeExpired,
		SlotUntil: database.RestoreSlotUntil, RestorableUntil: scheduled.PurgeAfter, RecordedAt: now}, summary)
	return restoreSlotExpired, nil
}

// recordNotice records a notice once. A failure is logged and does not undo
// the slot decision: an 'ending' notice is retried by the next sweep, while an
// 'expired' one is lost with its email, since the copy no longer expires.
func (w *RestoreSlotSweeper) recordNotice(ctx context.Context, notice RestoreSlotNotice, summary *RestoreSlotSummary) {
	store, ok := w.service.store.(RestoreSlotNoticeStore)
	if !ok {
		return
	}
	recorded, err := store.RecordRestoreSlotNotice(ctx, notice)
	if err != nil {
		w.logger.Warn("managed postgres restore slot notice not recorded", "database", notice.DatabaseID, "kind", notice.Kind, "error", err)
		return
	}
	if recorded {
		summary.Noticed++
	}
}

// mailNotices queues the email for each notice still unmailed and under a
// day old. A failed email does not fail the sweep; the next sweep retries it.
func (w *RestoreSlotSweeper) mailNotices(ctx context.Context, summary *RestoreSlotSummary) {
	store, ok := w.service.store.(RestoreSlotNoticeStore)
	if !ok || w.notify == nil {
		return
	}
	now := w.now()
	notices, err := store.UnmailedRestoreSlotNotices(ctx, now.Add(-api.InternalMailManagedPostgresRestoreSlotTTL), now, w.batchSize)
	if err != nil {
		summary.NotifyFailed++
		w.logger.Warn("managed postgres restore slot notices not listed", "error", err)
		return
	}
	for _, notice := range notices {
		if err := w.notify(ctx, notice); err != nil {
			summary.NotifyFailed++
			w.logger.Warn("managed postgres restore slot email not queued", "database", notice.DatabaseID, "kind", notice.Kind, "error", err)
		}
	}
}

// revokeBindings deletes the copy's live bindings. It reports whether any are
// still on their way out.
func (w *RestoreSlotSweeper) revokeBindings(ctx context.Context, database Database) (bool, error) {
	bindings, err := w.bindings.List(ctx, database.AccountID, database.ID)
	if err != nil {
		return false, err
	}
	pending := false
	for _, binding := range bindings {
		switch binding.State {
		case BindingStateDeleted:
			continue
		case BindingStateDeleting:
			pending = true
			continue
		}
		if _, err := w.bindings.Delete(ctx, database.AccountID, binding.ID); err != nil {
			return true, err
		}
		pending = true
	}
	return pending, nil
}

// restoreSlotRoom reports whether a slot holder fits inside its subject's
// database limit: the subject's other databases leave room for it.
func (s *Service) restoreSlotRoom(ctx context.Context, store RestoreSlotStore, subjectID string) (bool, error) {
	limit, err := s.maxDatabasesPerAccount(ctx, subjectID)
	if errors.Is(err, ErrQuotaExceeded) {
		// A plan without managed PostgreSQL has no room.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	count, err := store.CountSubjectDatabases(ctx, subjectID)
	if err != nil {
		return false, err
	}
	return count <= int64(limit), nil
}

// A database switch's copy becomes the app's database, so it must fit inside
// its owner's database limit: when a restore slot ends, the copy's bindings
// are revoked, the app's DATABASE_URL among them (ADR-20261031123259, GA
// amendment). Its restore marks the reservation, and both stores refuse it
// with ErrQuotaExceeded instead of granting the slot.
type withinLimitKey struct{}

func reserveWithinLimit(ctx context.Context) context.Context {
	return context.WithValue(ctx, withinLimitKey{}, true)
}

// restoreSlotAllowed reports whether a reservation made with ctx may take
// the restore slot.
func restoreSlotAllowed(ctx context.Context) bool {
	within, _ := ctx.Value(withinLimitKey{}).(bool)
	return !within
}

// DatabaseRoom reports how many databases count toward accountID's database
// limit (provisioning and restoring ones included) and the limit, so a
// restore that must fit inside it can be refused before anything changes.
func (s *Service) DatabaseRoom(ctx context.Context, accountID string) (int64, int, error) {
	store, ok := s.store.(RestoreSlotStore)
	if !ok {
		return 0, 0, ErrUnsupported
	}
	limit, err := s.reservationLimit(ctx, accountID)
	if err != nil {
		return 0, 0, err
	}
	count, err := store.CountSubjectDatabases(ctx, PolicySubject(ctx, accountID))
	if err != nil {
		return 0, 0, err
	}
	return count, limit, nil
}

// undeleteNeedsRoom refuses to recover a copy whose restore slot has ended
// unless its owner has room for it: the slot is never granted again, so it
// cannot extend itself (ADR-20261031123259 decision 5).
func (s *Service) undeleteNeedsRoom(ctx context.Context, accountID, databaseID string, now time.Time) error {
	store, ok := s.store.(RestoreSlotStore)
	if !ok {
		return nil
	}
	database, err := s.Get(ctx, accountID, databaseID)
	if err != nil {
		return err
	}
	if database.RestoreSlotUntil.IsZero() || now.Before(database.RestoreSlotUntil) {
		return nil
	}
	room, err := s.restoreSlotRoom(ctx, store, PolicySubject(ctx, accountID))
	if err != nil {
		return err
	}
	if !room {
		return ErrQuotaExceeded
	}
	return nil
}
