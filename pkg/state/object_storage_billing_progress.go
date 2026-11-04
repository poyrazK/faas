package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectStorageBillingProgress is the object-storage charge delivered so far
// for one account's UTC month (ADR-20261027093220). apid delivers it in daily
// steps and month close finishes it. A step first reserves the most it can
// deliver as PendingMillicents, then fixes PendingMillicents to its priced
// delta, publishes it, and adds it to DeliveredMillicents. meterd reserves
// live delivered and pending millicents under the account's overage cap.
type ObjectStorageBillingProgress struct {
	Provider            string
	AccountID           string
	PeriodStart         time.Time
	Mode                string
	DeliveredMillicents int64
	PendingMillicents   int64
	// PendingFixed marks a pending step whose delta is final and may already
	// be published; it is completed, never recomputed.
	PendingFixed bool
	// CreditedMillicents is what members were already charged for this
	// month's storage when an organization adopted it mid-month; the
	// organization owes only the rest. Only adoption writes it.
	CreditedMillicents int64
	// Version increments on every write; zero means no row yet.
	Version   int64
	UpdatedAt time.Time
}

// ObjectStorageBillingProgressStore persists progress rows. Every write is a
// compare-and-set on Version, so concurrent apids never deliver one step twice.
type ObjectStorageBillingProgressStore interface {
	// GetObjectStorageBillingProgress returns ErrNotFound when the month has
	// no row in that mode yet.
	GetObjectStorageBillingProgress(ctx context.Context, provider, accountID string, periodStart time.Time, mode string) (ObjectStorageBillingProgress, error)
	// SaveObjectStorageBillingProgress writes p if the stored row is still at
	// p.Version (zero inserts) and returns it at the next version. A lost race
	// returns ErrConflict. It never writes CreditedMillicents.
	SaveObjectStorageBillingProgress(ctx context.Context, p ObjectStorageBillingProgress) (ObjectStorageBillingProgress, error)
	// LiveObjectStorageMillicents is the account's live object-storage charge
	// for the month: delivered, and pending in steps not yet finished. The
	// overage cap reserves both; only delivered counts as billed
	// (ADR-20261027093220).
	LiveObjectStorageMillicents(ctx context.Context, accountID string, periodStart time.Time) (delivered, pending int64, err error)
}

func (p ObjectStorageBillingProgress) valid() bool {
	if p.Provider == "" || p.AccountID == "" || p.PeriodStart.IsZero() ||
		!p.PeriodStart.Equal(ObjectStoragePeriod(p.PeriodStart)) || p.Version < 0 ||
		p.DeliveredMillicents < 0 || p.PendingMillicents < 0 || p.CreditedMillicents < 0 ||
		p.CreditedMillicents > api.MaxObjectStoragePolicyValue ||
		p.DeliveredMillicents > api.MaxObjectStoragePolicyValue-p.PendingMillicents ||
		p.PendingFixed && p.PendingMillicents == 0 {
		return false
	}
	return p.Mode == ObjectStorageDeliveryShadow || p.Mode == ObjectStorageDeliveryLive
}

func validateObjectStorageBillingProgress(p ObjectStorageBillingProgress) (ObjectStorageBillingProgress, error) {
	p.PeriodStart = ObjectStoragePeriod(p.PeriodStart)
	if !p.valid() {
		return ObjectStorageBillingProgress{}, errors.New("state: invalid object storage billing progress")
	}
	return p, nil
}
