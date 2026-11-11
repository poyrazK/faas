package firecracker

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Qualification probes of the hold and the compute cap (ADR-1027 amendment
// 2026-11-10). Like the other probers they run only against disposable
// qualification resources and connect as an app does, through pgproxyd.

var (
	_ managedpostgres.DeletionHoldProber = (*Provider)(nil)
	_ managedpostgres.ComputeCapProber   = (*Provider)(nil)
)

const (
	// holdProbeWait bounds the wait for a held database to park.
	holdProbeWait = time.Minute
	// computeCapProbeActive is how long the compute probe keeps a session
	// open between its two readings of schedd's ledger. The ledger is
	// current, so a short window shows the growth.
	computeCapProbeActive = 20 * time.Second
	// computeCapProbeRaise is how far above the month's use the probe's
	// quota write sets the quota, so the database keeps running.
	computeCapProbeRaise = 3600
)

// ProbeDeletionHold holds a running database, observes it parked and a
// connection refused as suspended (08004), releases it and connects again.
func (p *Provider) ProbeDeletionHold(ctx context.Context, providerResourceID string, material managedpostgres.CredentialMaterial) (managedpostgres.DeletionHoldEvidence, error) {
	var evidence managedpostgres.DeletionHoldEvidence
	if err := p.ping(ctx, material); err != nil {
		return evidence, err
	}
	if err := p.HoldForDeletion(ctx, providerResourceID); err != nil {
		return evidence, err
	}
	released := false
	defer func() {
		if !released {
			_ = p.ReleaseDeletionHold(context.WithoutCancel(ctx), providerResourceID)
		}
	}()
	waitCtx, cancel := context.WithTimeout(ctx, holdProbeWait)
	evidence.SuspendedWhileHeld = p.waitCompute(waitCtx, providerResourceID, managedpostgres.ComputeStateSuspended, nil) == nil
	cancel()
	evidence.RefusedWhileHeld = refusedAsSuspended(p.ping(ctx, material))
	if err := p.ReleaseDeletionHold(ctx, providerResourceID); err != nil {
		return evidence, err
	}
	released = true
	evidence.ResumedAfterRelease = p.ping(ctx, material) == nil
	return evidence, nil
}

// refusedAsSuspended reports pgproxyd's answer to a held database's
// connection.
func refusedAsSuspended(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "08004"
}

// ProbeComputeCap checks what the cap sweep relies on: the period is the
// UTC month, the month's use grows while a session keeps the database
// running, by its vCPUs (and its standby's) per second, the hold suspends
// and refuses until released, and a quota write leaves the storage limit.
// Infrastructure failures return an error; a behaviour that does not match
// leaves its evidence field false.
func (p *Provider) ProbeComputeCap(ctx context.Context, providerResourceID string, material managedpostgres.CredentialMaterial) (managedpostgres.ComputeCapEvidence, error) {
	var evidence managedpostgres.ComputeCapEvidence
	rt, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: providerResourceID})
	if err != nil {
		return evidence, providerError(err)
	}
	vcpus := float64(rt.GetVcpuCount())
	if vcpus <= 0 {
		return evidence, managedpostgres.ErrUnavailable
	}
	maxCU := vcpus
	if rt.GetReplicated() {
		maxCU *= 2 // the standby's vCPUs count too
	}
	if err := p.probeComputeReport(ctx, providerResourceID, material, vcpus, maxCU, &evidence); err != nil {
		return evidence, err
	}
	hold, err := p.ProbeDeletionHold(ctx, providerResourceID, material)
	if err != nil {
		return evidence, err
	}
	evidence.SuspendedWhileHeld, evidence.RefusedWhileHeld, evidence.ResumedAfterRelease = hold.SuspendedWhileHeld, hold.RefusedWhileHeld, hold.ResumedAfterRelease
	observed, err := p.ObserveComputeCap(ctx, providerResourceID)
	if err != nil {
		return evidence, err
	}
	if err := p.SetComputeCap(ctx, providerResourceID, observed.UsedUnitSeconds+computeCapProbeRaise); err != nil {
		return evidence, err
	}
	after, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: providerResourceID})
	if err != nil {
		return evidence, providerError(err)
	}
	evidence.StorageLimitPreserved = after.GetVolumeBytes() == rt.GetVolumeBytes()
	return evidence, nil
}

// probeComputeReport reads the month's use twice while a session keeps the
// database running.
func (p *Provider) probeComputeReport(ctx context.Context, id string, material managedpostgres.CredentialMaterial, minCU, maxCU float64, evidence *managedpostgres.ComputeCapEvidence) error {
	conn, err := p.connect(ctx, material)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, "SELECT 1"); err != nil {
		return err
	}
	first, err := p.ObserveComputeCap(ctx, id)
	if err != nil {
		return err
	}
	firstAt := time.Now()
	now := firstAt.UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	evidence.PeriodStartsAtUTCMonth = first.PeriodStart.Sub(month).Abs() <= time.Hour
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(computeCapProbeActive):
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); err != nil {
		return err
	}
	last, err := p.ObserveComputeCap(ctx, id)
	if err != nil {
		return err
	}
	grew, elapsed := last.UsedUnitSeconds-first.UsedUnitSeconds, time.Since(firstAt).Seconds()
	evidence.ReportCurrent = grew > 0
	if grew > 0 && elapsed > 0 {
		evidence.ComputeUnitRatioPermille = int64(float64(grew) * 1000 / (elapsed * minCU))
	}
	evidence.ReportInComputeUnits = managedpostgres.ComputeUnitRatioAcceptable(evidence.ComputeUnitRatioPermille, minCU, maxCU)
	return nil
}
