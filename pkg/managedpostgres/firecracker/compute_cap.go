package firecracker

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Compute caps and holds (ADR-1027 amendment 2026-11-10, ADR-20261025094915).
// apid's cap sweep and delete grace period hold a native database through
// schedd, which parks it and refuses its wakes; the quota is schedd's
// backstop between sweeps. Use is read from schedd's ledger, which is
// current (its open interval counts until now), so the driver is not a
// ComputeUseReporter: the sweep takes its counters as they are. A restore
// is its own database with its own ledger, so every database is its own
// quota scope.

var (
	_ managedpostgres.ComputeCapProvider  = (*Provider)(nil)
	_ managedpostgres.ComputeHoldProvider = (*Provider)(nil)
)

// ObserveComputeCap reports the database's compute this UTC month and its
// quota.
func (p *Provider) ObserveComputeCap(ctx context.Context, providerResourceID string) (managedpostgres.ComputeCapObservation, error) {
	return p.observeComputeCap(ctx, providerResourceID, time.Now())
}

func (p *Provider) observeComputeCap(ctx context.Context, providerResourceID string, now time.Time) (managedpostgres.ComputeCapObservation, error) {
	if providerResourceID == "" {
		return managedpostgres.ComputeCapObservation{}, managedpostgres.ErrInvalid
	}
	rt, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: providerResourceID})
	if err != nil {
		return managedpostgres.ComputeCapObservation{}, providerError(err)
	}
	now = now.UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	// A window must not be empty; the month's first second has used none.
	to := now
	if !to.After(month) {
		to = month.Add(time.Second)
	}
	usage, err := p.scheduler.DatabaseUsage(ctx, &scheddpb.DatabaseUsageRequest{
		DatabaseId: providerResourceID, From: timestamppb.New(month), To: timestamppb.New(to),
	})
	if err != nil {
		return managedpostgres.ComputeCapObservation{}, providerError(err)
	}
	return managedpostgres.ComputeCapObservation{
		Scope:            providerResourceID,
		PeriodStart:      month,
		UsedUnitSeconds:  usage.GetComputeUnitSeconds(),
		QuotaUnitSeconds: rt.GetComputeQuotaUnitSeconds(),
	}, nil
}

// SetComputeCap writes the database's quota and checks schedd's answer,
// since an accepted call alone is not evidence.
func (p *Provider) SetComputeCap(ctx context.Context, providerResourceID string, quotaUnitSeconds int64) error {
	if providerResourceID == "" || quotaUnitSeconds <= 0 {
		return managedpostgres.ErrInvalid
	}
	rt, err := p.scheduler.SetDatabaseComputeQuota(ctx, &scheddpb.SetDatabaseComputeQuotaRequest{
		DatabaseId: providerResourceID, QuotaUnitSeconds: quotaUnitSeconds,
	})
	if err != nil {
		return providerError(err)
	}
	if rt.GetComputeQuotaUnitSeconds() != quotaUnitSeconds {
		return fmt.Errorf("%w: schedd did not record the compute quota", managedpostgres.ErrUnavailable)
	}
	return nil
}

// HoldForDeletion holds the database: schedd parks it and refuses its
// wakes, so it runs nothing and accepts no connection until released.
func (p *Provider) HoldForDeletion(ctx context.Context, dataResourceID string) error {
	return p.setHeld(ctx, dataResourceID, true)
}

// ReleaseDeletionHold lets the database's next connection wake it.
func (p *Provider) ReleaseDeletionHold(ctx context.Context, dataResourceID string) error {
	return p.setHeld(ctx, dataResourceID, false)
}

// ComputeHeld reports whether schedd holds the database.
func (p *Provider) ComputeHeld(ctx context.Context, providerResourceID string) (bool, error) {
	if providerResourceID == "" {
		return false, managedpostgres.ErrInvalid
	}
	rt, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: providerResourceID})
	if err != nil {
		return false, providerError(err)
	}
	return rt.GetHeld(), nil
}

func (p *Provider) setHeld(ctx context.Context, id string, held bool) error {
	if id == "" {
		return managedpostgres.ErrInvalid
	}
	rt, err := p.scheduler.HoldDatabase(ctx, &scheddpb.HoldDatabaseRequest{DatabaseId: id, Held: held})
	if err != nil {
		return providerError(err)
	}
	if rt.GetHeld() != held {
		return fmt.Errorf("%w: schedd did not record the hold", managedpostgres.ErrUnavailable)
	}
	return nil
}
