package managedpostgres

import "context"

// IdleTail estimates the compute a scale-to-zero database spent idle before
// suspending in one usage window: each idle suspend follows a full suspend
// timeout with no activity, at the compute's minimum size. It is telemetry
// for tuning the suspend timeout and the class minimums, never billing.
type IdleTail struct {
	// Suspends counts the window's suspends of the database's read-write
	// computes.
	Suspends int64
	// ComputeUnitSeconds is Σ suspend timeout × minimum CU over those
	// suspends.
	ComputeUnitSeconds int64
}

// IdleTailReporter is the optional provider surface behind IdleTail.
type IdleTailReporter interface {
	IdleTail(ctx context.Context, providerResourceID string, window UsageWindow) (IdleTail, error)
}

// Idle tail read outcomes.
const (
	IdleTailRead        = "read"
	IdleTailNoCompute   = "no_compute"
	IdleTailUnsupported = "unsupported"
	IdleTailUnavailable = "unavailable"
)

// IdleTailObservation is one scale-to-zero database's first collected
// usage window: the compute it used and the idle tail inside it. Correction
// replays are not observed, so each window counts once per process.
type IdleTailObservation struct {
	Class              ServiceClass
	Outcome            string
	ComputeUnitSeconds int64
	Tail               IdleTail
}

// observeIdleTail reads the window's idle tail when the database computed in
// it. A failed read never fails usage collection.
func (c *UsageCollector) observeIdleTail(ctx context.Context, database Database, backend Backend, usage Usage) {
	if c.idleTailObserver == nil || !database.Spec.ScaleToZero {
		return
	}
	observation := IdleTailObservation{Class: database.Spec.Class, Outcome: IdleTailNoCompute}
	for _, reading := range usage.Readings {
		if reading.Meter == MeterComputeUnitSeconds {
			observation.ComputeUnitSeconds = reading.Quantity
		}
	}
	reporter, ok := backend.Provider.(IdleTailReporter)
	switch {
	case !ok:
		observation.Outcome = IdleTailUnsupported
	case observation.ComputeUnitSeconds > 0:
		tail, err := reporter.IdleTail(ctx, database.ProviderResourceID, usage.Window)
		if err != nil {
			observation.Outcome = IdleTailUnavailable
			c.logger.Debug("managed postgres idle tail unavailable", "database_id", database.ID, "error", err)
			break
		}
		observation.Outcome, observation.Tail = IdleTailRead, tail
	}
	c.idleTailObserver(observation)
}
