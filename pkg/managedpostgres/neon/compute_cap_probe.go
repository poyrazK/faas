package neon

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.ComputeCapProber = (*Provider)(nil)

// Compute cap probe timings. Neon's counters lag, so the probe holds the
// compute busy for a measured interval and waits for the counters to show it.
const (
	computeCapProbeHold          = 90 * time.Second
	computeCapProbeMinimumActive = 30
	computeCapProbeCounterWait   = 5 * time.Minute
	computeCapProbeSuspendWait   = 3 * time.Minute
	computeCapProbeRefuseWait    = 30 * time.Second
	computeCapProbeResumeWait    = 2 * time.Minute
	computeCapProbePoll          = 5 * time.Second
	computeCapProbeRaise         = 3600
)

// ProbeComputeCap runs only in operator qualification, on a disposable
// resource (ADR-20261025094915 decision 10). It measures the compute counter
// against the compute's size, suspends the compute with a quota at the
// counter, checks a connection is refused, raises the quota, and checks the
// next connection wakes it. Infrastructure failures return an error; a
// behaviour that does not match leaves its evidence field false.
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

	if err := probeHold(ctx, dsn, computeCapProbeHold); err != nil {
		return evidence, err
	}
	after, err := p.awaitComputeCounters(ctx, ref.projectID, before)
	if err != nil {
		return evidence, err
	}
	activeDelta := after.ActiveTimeSeconds - before.ActiveTimeSeconds
	computeDelta := after.ComputeTimeSeconds - before.ComputeTimeSeconds
	if activeDelta > 0 {
		evidence.ComputeUnitRatioPermille = int64(float64(computeDelta) * 1000 / (float64(activeDelta) * profile.minimumCU))
	}
	evidence.CounterInComputeUnits = managedpostgres.ComputeUnitRatioAcceptable(evidence.ComputeUnitRatioPermille, profile.minimumCU, profile.maximumCU)

	if err := p.SetComputeCap(ctx, providerResourceID, max(after.ComputeTimeSeconds, 1)); err != nil {
		return evidence, err
	}
	evidence.SuspendedAtQuota = p.awaitComputeState(ctx, providerResourceID, managedpostgres.ComputeStateSuspended, computeCapProbeSuspendWait)
	refuseCtx, cancel := context.WithTimeout(ctx, computeCapProbeRefuseWait)
	evidence.RefusedWhileSuspended = probeQuery(refuseCtx, dsn) != nil
	cancel()

	current, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return evidence, err
	}
	if err := p.SetComputeCap(ctx, providerResourceID, current.ComputeTimeSeconds+computeCapProbeRaise); err != nil {
		return evidence, err
	}
	evidence.ResumedAfterRaise = awaitQuery(ctx, dsn, computeCapProbeResumeWait)
	final, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return evidence, err
	}
	evidence.StorageLimitPreserved = sameQuota(final.Settings.Quota.LogicalSizeBytes, before.Settings.Quota.LogicalSizeBytes)
	return evidence, nil
}

// awaitComputeCounters waits until the project's active-time counter shows
// the held interval.
func (p *Provider) awaitComputeCounters(ctx context.Context, projectID string, before computeCapProject) (computeCapProject, error) {
	deadline := time.Now().Add(computeCapProbeCounterWait)
	for {
		current, err := p.readComputeCapProject(ctx, projectID)
		if err != nil {
			return computeCapProject{}, err
		}
		if current.ActiveTimeSeconds-before.ActiveTimeSeconds >= computeCapProbeMinimumActive || time.Now().After(deadline) {
			return current, nil
		}
		if err := sleepContext(ctx, computeCapProbePoll); err != nil {
			return computeCapProject{}, err
		}
	}
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
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
