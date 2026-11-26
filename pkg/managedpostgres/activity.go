package managedpostgres

import (
	"context"
	"sort"
	"time"
)

// Why a database is awake (ADR-20261125231222): the usage sweep records each
// start of a database's compute, with its likely cause, from the same
// provider read that prices the idle tail.

// ComputeEventKind is a compute start or suspend read from the provider.
type ComputeEventKind string

const (
	ComputeEventStart   ComputeEventKind = "start"
	ComputeEventSuspend ComputeEventKind = "suspend"
)

// ComputeEvent is one finished start or suspend of a database's compute.
// OperationID is the provider's, unique within the database.
type ComputeEvent struct {
	OperationID string
	Kind        ComputeEventKind
	At          time.Time
}

// Compute start causes, in the order apid tries them.
const (
	CauseAppWake    = "app_wake"
	CauseCron       = "cron"
	CauseRequest    = "request"
	CauseAppStart   = "app_start"
	CauseRunningApp = "running_app"
	CauseExternal   = "external"
)

// ComputeStart is one recorded start. SuspendedAt is zero while the compute
// runs or its suspend has not been read yet.
type ComputeStart struct {
	OperationID string
	StartedAt   time.Time
	SuspendedAt time.Time
	Cause       string
	AppID       string
	AppSlug     string
	Detail      string
}

// ComputeStartCause is one cause's share of a period's starts. AwakeSeconds
// counts only the part of each start inside the period, an open one to now.
type ComputeStartCause struct {
	Cause          string
	AppID          string
	AppSlug        string
	Detail         string
	Starts         int
	AwakeSeconds   int64
	FirstStartedAt time.Time
}

// DatabaseActivityTotals is one database's starts, awake time and compute in
// a period.
type DatabaseActivityTotals struct {
	DatabaseID         string
	Starts             int
	AwakeSeconds       int64
	ComputeUnitSeconds int64
}

// ComputeActivityStore keeps the recorded starts.
type ComputeActivityStore interface {
	// RecordComputeEvents records the events oldest first: a start with its
	// cause (an already recorded one is left alone), a suspend closing the
	// latest start before it. It reports how many starts it recorded.
	RecordComputeEvents(ctx context.Context, databaseID string, events []ComputeEvent) (int, error)
	// PruneComputeStarts deletes up to limit starts, and up to limit
	// wake-with-app claims, made before a time; it reports the larger count.
	PruneComputeStarts(ctx context.Context, before time.Time, limit int) (int64, error)
	ComputeStarts(ctx context.Context, databaseID string, since time.Time, limit int) ([]ComputeStart, error)
	ComputeStartCauses(ctx context.Context, databaseID string, since, now time.Time) ([]ComputeStartCause, error)
	// AccountActivityTotals reports each of the account's databases with
	// compute or starts since a time.
	AccountActivityTotals(ctx context.Context, accountID string, since, now time.Time) (map[string]DatabaseActivityTotals, error)
}

// sortComputeEvents orders events oldest first, a start before a suspend at
// the same instant.
func sortComputeEvents(events []ComputeEvent) []ComputeEvent {
	out := append([]ComputeEvent(nil), events...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].Kind == ComputeEventStart && out[j].Kind != ComputeEventStart
	})
	return out
}

// computeStartsPruneBatch bounds one prune statement, and
// computeStartsPruneBatches the statements of one hourly pass (1M rows).
const (
	computeStartsPruneBatch   = 5000
	computeStartsPruneBatches = 200
)

// recordComputeActivity stores a window's events. A failure is logged: the
// starts are diagnosis, and the next read of a later window cannot restore
// them, so the gap shows in the covered period instead.
func (c *UsageCollector) recordComputeActivity(ctx context.Context, item pendingIdleTail, events []ComputeEvent) {
	store, ok := c.store.(ComputeActivityStore)
	if !ok || len(events) == 0 {
		return
	}
	if _, err := store.RecordComputeEvents(ctx, item.database.ID, events); err != nil {
		c.logger.Debug("managed postgres compute starts not recorded", "database_id", item.database.ID, "error", err)
	}
}

// pruneComputeActivity deletes starts past retention, at most once an hour.
func (c *UsageCollector) pruneComputeActivity(ctx context.Context, retention time.Duration) {
	store, ok := c.store.(ComputeActivityStore)
	now := c.now()
	if !ok || now.Sub(c.activityPrunedAt) < time.Hour {
		return
	}
	c.activityPrunedAt = now
	for range computeStartsPruneBatches {
		n, err := store.PruneComputeStarts(ctx, now.Add(-retention), computeStartsPruneBatch)
		if err != nil {
			c.logger.Debug("managed postgres compute starts not pruned", "error", err)
			return
		}
		if n < computeStartsPruneBatch {
			return
		}
	}
}
