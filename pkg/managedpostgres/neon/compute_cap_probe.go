package neon

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.ComputeCapProber = (*Provider)(nil)

// Compute cap probe timings (ADR-20261025094915 amendment 2026-11-05). The
// probe keeps the compute active for computeCapProbeHold and reads the
// consumption report twice inside that time, so both readings trail the
// report's delay by the same amount and their difference is the compute
// used between them. A report more than computeCapProbeFirstRead behind
// shows nothing between them. Eight minutes between the readings keeps a
// report that moves in whole minutes inside the ratio's bounds.
const (
	computeCapProbeHold      = 12 * time.Minute
	computeCapProbeFirstRead = 3 * time.Minute
	computeCapProbeLastRead  = 11 * time.Minute
	computeCapProbeRaise     = 3600
	computeCapProbePoll      = 5 * time.Second
)

// ProbeComputeCap runs only in operator qualification, on a disposable
// resource (ADR-20261025094915 decision 10, amendment 2026-11-05). It checks
// what enforcement relies on: the consumption period starts at the UTC month,
// the consumption report is current and counts compute-unit seconds, the
// hold suspends the compute and refuses a connection until released, and a
// quota write keeps the storage limit. Neon's own quota enforcement waits for
// a counter that can stay put for over an hour, so the probe does not wait
// for it. Infrastructure failures return an error; a behaviour that does not
// match leaves its evidence field false.
func (p *Provider) ProbeComputeCap(ctx context.Context, providerResourceID string, material managedpostgres.CredentialMaterial) (managedpostgres.ComputeCapEvidence, error) {
	var evidence managedpostgres.ComputeCapEvidence
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return evidence, err
	}
	dsn, err := probeDSN(material)
	if err != nil {
		return evidence, err
	}
	observed, err := p.Inspect(ctx, providerResourceID)
	if err != nil {
		return evidence, err
	}
	profile, ok := profiles[observed.Spec.Class]
	if !ok {
		return evidence, managedpostgres.ErrUnsupported
	}
	before, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return evidence, err
	}
	period, err := time.Parse(time.RFC3339, before.ConsumptionPeriodStart)
	if err != nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	month := time.Date(p.now().UTC().Year(), p.now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	evidence.PeriodStartsAtUTCMonth = period.UTC().Sub(month).Abs() <= time.Hour

	if err := p.probeComputeReport(ctx, providerResourceID, dsn, profile, &evidence); err != nil {
		return evidence, err
	}
	hold, err := p.ProbeDeletionHold(ctx, providerResourceID, material)
	if err != nil {
		return evidence, err
	}
	evidence.SuspendedWhileHeld, evidence.RefusedWhileHeld, evidence.ResumedAfterRelease = hold.SuspendedWhileHeld, hold.RefusedWhileHeld, hold.ResumedAfterRelease

	if err := p.SetComputeCap(ctx, providerResourceID, before.ComputeTimeSeconds+computeCapProbeRaise); err != nil {
		return evidence, err
	}
	final, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return evidence, err
	}
	evidence.StorageLimitPreserved = sameQuota(final.Settings.Quota.LogicalSizeBytes, before.Settings.Quota.LogicalSizeBytes)
	return evidence, nil
}

// probeComputeReport holds the compute active and reads the consumption
// report twice while it is, the open hour included.
func (p *Provider) probeComputeReport(ctx context.Context, providerResourceID, dsn string, profile classProfile, evidence *managedpostgres.ComputeCapEvidence) error {
	started := p.now().UTC()
	from := started.Truncate(time.Hour)
	held := make(chan error, 1)
	go func() { held <- probeHold(ctx, dsn, computeCapProbeHold) }()
	read := func(at time.Duration) (int64, time.Time, error) {
		if err := sleepContext(ctx, time.Until(started.Add(at))); err != nil {
			return 0, time.Time{}, err
		}
		used, err := p.ComputeUnitSecondsSince(ctx, providerResourceID, from)
		return used, time.Now(), err
	}
	first, firstAt, err := read(computeCapProbeFirstRead)
	if err != nil {
		<-held
		return err
	}
	last, lastAt, err := read(computeCapProbeLastRead)
	if holdErr := <-held; err == nil {
		err = holdErr
	}
	if err != nil {
		return err
	}
	grew, elapsed := last-first, lastAt.Sub(firstAt).Seconds()
	evidence.ReportCurrent = grew > 0
	if grew > 0 && elapsed > 0 {
		evidence.ComputeUnitRatioPermille = int64(float64(grew) * 1000 / (elapsed * profile.minimumCU))
	}
	evidence.ReportInComputeUnits = managedpostgres.ComputeUnitRatioAcceptable(evidence.ComputeUnitRatioPermille, profile.minimumCU, profile.maximumCU)
	return nil
}

func (p *Provider) awaitComputeState(ctx context.Context, providerResourceID string, want managedpostgres.ComputeState, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		observed, err := p.Inspect(ctx, providerResourceID)
		if err == nil && observed.ComputeState == want {
			return true
		}
		if time.Now().After(deadline) || sleepContext(ctx, computeCapProbePoll) != nil {
			return false
		}
	}
}

func awaitQuery(ctx context.Context, dsn string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := probeQuery(attempt, dsn)
		cancel()
		if err == nil {
			return true
		}
		if time.Now().After(deadline) || sleepContext(ctx, computeCapProbePoll) != nil {
			return false
		}
	}
}

// probeHold keeps the compute active for hold.
func probeHold(ctx context.Context, dsn string, hold time.Duration) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, "SELECT pg_sleep($1)", hold.Seconds())
	return err
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
