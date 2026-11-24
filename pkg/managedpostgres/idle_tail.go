package managedpostgres

import "context"

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
}

// IdleTailReporter is the optional provider surface behind IdleTail.
type IdleTailReporter interface {
	IdleTail(ctx context.Context, providerResourceID string, window UsageWindow) (IdleTail, error)
}

// Idle tail read outcomes. Only read and no_compute windows count toward the
// share, so its two sides always cover the same windows.
const (
	IdleTailRead        = "read"
	IdleTailNoCompute   = "no_compute"
	IdleTailUnsupported = "unsupported"
	IdleTailUnavailable = "unavailable"
	// IdleTailSkipped is a window the pass had no budget for, or that came
	// after a failed read ended the pass.
	IdleTailSkipped = "skipped"
)

// maximumIdleTailReadsPerSweep bounds the provider reads one pass makes, so
// the telemetry never competes with billing and lifecycle requests for the
// provider's rate limit.
const maximumIdleTailReadsPerSweep = 60

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

// queueIdleTails keeps the sweep's newly collected latest windows of
// scale-to-zero databases for readIdleTails.
func (c *UsageCollector) queueIdleTails(work []*usageCollectionWork) {
	c.pendingIdleTails = nil
	if c.idleTailObserver == nil {
		return
	}
	for _, item := range work {
		if item.latest != nil && item.database.Spec.ScaleToZero {
			c.pendingIdleTails = append(c.pendingIdleTails, pendingIdleTail{item.database, item.backend, *item.latest})
		}
	}
}

// readIdleTails observes the queued windows after billing collection and
// AfterSweep are done. It reads at most maximumIdleTailReadsPerSweep windows,
// only those with compute, and stops at the first failed read: a rate-limited
// provider gets no more telemetry requests this sweep.
func (c *UsageCollector) readIdleTails(ctx context.Context) {
	pending := c.pendingIdleTails
	c.pendingIdleTails = nil
	reads, stopped := 0, false
	for _, item := range pending {
		observation := IdleTailObservation{Class: item.database.Spec.Class, Outcome: IdleTailNoCompute}
		for _, reading := range item.usage.Readings {
			if reading.Meter == MeterComputeUnitSeconds {
				observation.ComputeUnitSeconds = reading.Quantity
			}
		}
		reporter, ok := item.backend.Provider.(IdleTailReporter)
		switch {
		case observation.ComputeUnitSeconds <= 0:
		case !ok:
			observation.Outcome = IdleTailUnsupported
		case stopped || reads >= maximumIdleTailReadsPerSweep || ctx.Err() != nil:
			observation.Outcome = IdleTailSkipped
		default:
			reads++
			tail, err := reporter.IdleTail(ctx, item.database.ProviderResourceID, item.usage.Window)
			if err != nil {
				observation.Outcome, stopped = IdleTailUnavailable, true
				c.logger.Debug("managed postgres idle tail unavailable", "database_id", item.database.ID, "error", err)
				break
			}
			observation.Outcome, observation.Tail = IdleTailRead, tail
		}
		c.idleTailObserver(observation)
	}
}
