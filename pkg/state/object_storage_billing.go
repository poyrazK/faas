package state

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectStorageBillingRecord is the immutable month-close snapshot that a
// billing adapter can later turn into an invoice line item. Rates are copied
// into the row so changing the operator's pricing configuration cannot change
// an already-finalized period.
type ObjectStorageBillingRecord struct {
	ID                           string
	AccountID                    string
	PeriodStart                  time.Time
	PeriodEnd                    time.Time
	Currency                     string
	StoredByteHours              int64
	RequestCount                 int64
	EgressBytes                  int64
	ProviderCostMillicents       int64
	StorageMillicentsPerGiBMonth int64
	RequestsMillicentsPerMillion int64
	EgressMillicentsPerGiB       int64
	StorageMillicents            int64
	RequestsMillicents           int64
	EgressMillicents             int64
	TotalMillicents              int64
	FinalizedAt                  time.Time
}

func (r ObjectStorageBillingRecord) pricing() api.ObjectStoragePricing {
	return api.ObjectStoragePricing{
		Currency:                     r.Currency,
		StorageMillicentsPerGiBMonth: r.StorageMillicentsPerGiBMonth,
		RequestsMillicentsPerMillion: r.RequestsMillicentsPerMillion,
		EgressMillicentsPerGiB:       r.EgressMillicentsPerGiB,
	}
}

func (r ObjectStorageBillingRecord) valid() bool {
	if r.AccountID == "" || r.PeriodStart.IsZero() || r.PeriodEnd.IsZero() ||
		!r.PeriodStart.Equal(ObjectStoragePeriod(r.PeriodStart)) ||
		!r.PeriodEnd.Equal(r.PeriodStart.AddDate(0, 1, 0)) ||
		!r.pricing().Valid() || r.FinalizedAt.IsZero() {
		return false
	}
	for _, v := range []int64{
		r.StoredByteHours, r.RequestCount, r.EgressBytes, r.ProviderCostMillicents,
		r.StorageMillicentsPerGiBMonth, r.RequestsMillicentsPerMillion, r.EgressMillicentsPerGiB,
		r.StorageMillicents, r.RequestsMillicents, r.EgressMillicents, r.TotalMillicents,
	} {
		if v < 0 || v > api.MaxObjectStoragePolicyValue {
			return false
		}
	}
	if r.StorageMillicents > api.MaxObjectStoragePolicyValue-r.RequestsMillicents {
		return false
	}
	sum := r.StorageMillicents + r.RequestsMillicents
	if sum > api.MaxObjectStoragePolicyValue-r.EgressMillicents {
		return false
	}
	return r.TotalMillicents == sum+r.EgressMillicents
}

func sameObjectStorageBillingRecord(a, b ObjectStorageBillingRecord) bool {
	// IDs and timestamps are store metadata; the financial snapshot itself
	// must match exactly before an idempotent retry is accepted.
	a.ID, b.ID = "", ""
	a.FinalizedAt, b.FinalizedAt = time.Time{}, time.Time{}
	return a == b
}

func normalizeObjectStorageBillingRecord(r ObjectStorageBillingRecord) ObjectStorageBillingRecord {
	if !r.PeriodStart.IsZero() {
		r.PeriodStart = ObjectStoragePeriod(r.PeriodStart)
		r.PeriodEnd = r.PeriodStart.AddDate(0, 1, 0)
	}
	r.Currency = r.pricing().Currency
	return r
}

func validateObjectStorageBillingRecord(r ObjectStorageBillingRecord) error {
	if !r.valid() {
		return errors.New("state: invalid object storage billing record")
	}
	return nil
}
