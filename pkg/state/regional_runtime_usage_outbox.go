package state

// adr: 802
import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type RegionalRuntimeUsageOutboxItem struct {
	ID          int64
	Usage       runtimequota.RuntimeUsage
	Envelope    runtimequota.Envelope
	CreatedAt   time.Time
	Ack         *runtimequota.UsageImportAck
	AckEnvelope *runtimequota.Envelope
}

type regionalRuntimeUsageQueued struct {
	usage    runtimequota.RuntimeUsage
	envelope runtimequota.Envelope
}
type regionalRuntimeUsageImported struct {
	ack      runtimequota.UsageImportAck
	envelope runtimequota.Envelope
}

type RegionalRuntimeUsageOutboxStore interface {
	queueRegionalRuntimeUsage(context.Context, regionalRuntimeUsageQueued) (RegionalRuntimeUsageOutboxItem, error)
	ackRegionalRuntimeUsage(context.Context, regionalRuntimeUsageImported) (RegionalRuntimeUsageOutboxItem, error)
	RegionalRuntimeUsageOutbox(context.Context, string, string, time.Time) (RegionalRuntimeUsageOutboxItem, error)
	ListPendingRegionalRuntimeUsage(context.Context, string, int64, int) ([]RegionalRuntimeUsageOutboxItem, error)
}

// This transport boundary accepts an already signed complete-minute assertion.
// It does not produce or seal one. The future producer must prove coverage from
// retained original history before signing; customer handlers have no writer.
func QueueRegionalRuntimeUsage(ctx context.Context, s RegionalRuntimeUsageOutboxStore, cells runtimequota.Trust, e runtimequota.Envelope, now time.Time) (RegionalRuntimeUsageOutboxItem, error) {
	if s == nil {
		return RegionalRuntimeUsageOutboxItem{}, runtimequota.ErrTrust
	}
	u, err := cells.VerifyRuntimeUsage(e)
	if err != nil {
		return RegionalRuntimeUsageOutboxItem{}, err
	}
	if u.ObservedAtMicros > now.UTC().UnixMicro() {
		return RegionalRuntimeUsageOutboxItem{}, ErrInvalidArgument
	}
	return s.queueRegionalRuntimeUsage(ctx, regionalRuntimeUsageQueued{u, e})
}

func AcknowledgeRegionalRuntimeUsage(ctx context.Context, s RegionalRuntimeUsageOutboxStore, directories runtimequota.Trust, e runtimequota.Envelope, now time.Time) (RegionalRuntimeUsageOutboxItem, error) {
	if s == nil {
		return RegionalRuntimeUsageOutboxItem{}, runtimequota.ErrTrust
	}
	a, err := directories.VerifyUsageImportAck(e)
	if err != nil {
		return RegionalRuntimeUsageOutboxItem{}, err
	}
	if a.ImportedAtMicros > now.UTC().UnixMicro() {
		return RegionalRuntimeUsageOutboxItem{}, ErrInvalidArgument
	}
	return s.ackRegionalRuntimeUsage(ctx, regionalRuntimeUsageImported{a, e})
}

func regionalUsageOutboxKey(u runtimequota.RuntimeUsage) string {
	return globalRuntimeUsageRowKey(u.Grant.Binding.RegionalAppID+":"+u.Grant.AllocationID, time.UnixMicro(u.MinuteMicros))
}

func validRegionalUsagePage(cell string, after int64, limit int) bool {
	return (cell == "" || validRegionalReleaseIDs(cell, cell, cell)) && after >= 0 && limit > 0 && limit <= api.RegionalRuntimeUsagePageMax
}

func checkRegionalUsageOrigin(u runtimequota.RuntimeUsage, r RegionalRuntimeQuotaReceipt) error {
	if u.Validate() != nil || u.Grant != r.Grant || r.GrantEnvelope == nil {
		return ErrConflict
	}
	return nil // Retirement and collection cannot substitute today's authority.
}

func cloneRegionalUsageOutbox(r RegionalRuntimeUsageOutboxItem) RegionalRuntimeUsageOutboxItem {
	if r.Ack != nil {
		a := *r.Ack
		r.Ack = &a
	}
	if r.AckEnvelope != nil {
		e := *r.AckEnvelope
		r.AckEnvelope = &e
	}
	return r
}
