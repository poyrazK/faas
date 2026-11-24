package managedpostgres

import (
	"context"
	"errors"
)

// IdleTail estimates the compute a scale-to-zero database spent idle before
// suspending in one usage window: each idle suspend follows a full suspend
// timeout with no activity, at the compute's minimum size. It is telemetry
// for tuning the suspend timeout and the class minimums, never billing.
type IdleTail struct {
	// Suspends counts the window's idle suspends of the database's compute.
	Suspends int64
	// ComputeUnitSeconds is Σ suspend timeout × minimum CU over those
	// suspends.
	ComputeUnitSeconds int64
	// Requests is how many provider requests the read sent.
	Requests int
}

// IdleTailReporter is the optional provider surface behind IdleTail. A read
// sends at most maxRequests requests and reports the number it sent, also
// with an error. ErrUnsupported means this database or window cannot be
// priced; any other error means the provider could not be read.
type IdleTailReporter interface {
	IdleTail(ctx context.Context, providerResourceID string, window UsageWindow, maxRequests int) (IdleTail, error)
}

// Idle tail read outcomes. Only read and no_compute windows count toward the
// share, so its two sides always cover the same windows.
const (
	IdleTailRead        = "read"
	IdleTailNoCompute   = "no_compute"
	IdleTailUnsupported = "unsupported"
	IdleTailUnavailable = "unavailable"
	// IdleTailSkipped is a queued window that a newer window replaced before
	// the pass reached it.
	IdleTailSkipped = "skipped"
)

const (
	// idleTailRequestsPerSweep bounds one pass's provider requests. The
	// requests are paced as background work, and windows the pass does not
	// reach wait for the next sweep, so at the default five-minute interval
	// the telemetry sends at most 12 requests a minute.
	idleTailRequestsPerSweep = 60
	// idleTailRequestsPerRead is one read's ceiling: the compute list and at
	// most three pages of operations.
	idleTailRequestsPerRead = 4
)

// IdleTailObservation is one scale-to-zero database's latest window,
// collected for the first time: the compute it used and the idle tail inside
// it. Catch-up and correction windows are not observed.
type IdleTailObservation struct {
	Class              ServiceClass
	Outcome            string
	ComputeUnitSeconds int64
	Tail               IdleTail
}

type pendingIdleTail struct {
	database Database
	backend  Backend
	usage    Usage
}

// queueIdleTails adds the sweep's newly collected latest windows of
// scale-to-zero databases to the windows earlier passes did not reach. A
// database's newer window replaces its older one, which is skipped.
func (c *UsageCollector) queueIdleTails(work []*usageCollectionWork) {
	if c.idleTailObserver == nil {
		return
	}
	queued := make(map[string]int, len(c.pendingIdleTails))
	for i, item := range c.pendingIdleTails {
		queued[item.database.ID] = i
	}
	for _, item := range work {
		if item.latest == nil || !item.database.Spec.ScaleToZero {
			continue
		}
		next := pendingIdleTail{item.database, item.backend, *item.latest}
		if i, ok := queued[item.database.ID]; ok {
			c.idleTailObserver(idleTailObservation(c.pendingIdleTails[i], IdleTailSkipped))
			c.pendingIdleTails[i] = next
			continue
		}
		queued[item.database.ID] = len(c.pendingIdleTails)
		c.pendingIdleTails = append(c.pendingIdleTails, next)
	}
}

func idleTailObservation(item pendingIdleTail, outcome string) IdleTailObservation {
	observation := IdleTailObservation{Class: item.database.Spec.Class, Outcome: outcome}
	for _, reading := range item.usage.Readings {
		if reading.Meter == MeterComputeUnitSeconds {
			observation.ComputeUnitSeconds = reading.Quantity
		}
	}
	return observation
}

// readIdleTails observes queued windows after billing collection and
// AfterSweep are done, within idleTailRequestsPerSweep paced background
// requests. Windows it does not reach stay queued. A provider that could not
// be read ends the pass.
func (c *UsageCollector) readIdleTails(ctx context.Context) {
	ctx = WithBackgroundRequests(ctx)
	budget := idleTailRequestsPerSweep
	done := 0
	for _, item := range c.pendingIdleTails {
		if ctx.Err() != nil {
			break
		}
		observation := idleTailObservation(item, IdleTailNoCompute)
		reporter, ok := item.backend.Provider.(IdleTailReporter)
		if observation.ComputeUnitSeconds > 0 && !ok {
			observation.Outcome = IdleTailUnsupported
		}
		if observation.ComputeUnitSeconds > 0 && ok {
			if budget < idleTailRequestsPerRead {
				break
			}
			tail, err := reporter.IdleTail(ctx, item.database.ProviderResourceID, item.usage.Window, idleTailRequestsPerRead)
			budget -= max(tail.Requests, 1)
			switch {
			case err == nil:
				observation.Outcome, observation.Tail = IdleTailRead, tail
			case errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound):
				observation.Outcome = IdleTailUnsupported
			default:
				c.logger.Debug("managed postgres idle tail unavailable", "database_id", item.database.ID, "error", err)
				c.idleTailObserver(idleTailObservation(item, IdleTailUnavailable))
				done++
				c.pendingIdleTails = c.pendingIdleTails[done:]
				return
			}
		}
		c.idleTailObserver(observation)
		done++
	}
	c.pendingIdleTails = c.pendingIdleTails[done:]
}
