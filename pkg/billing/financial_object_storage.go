package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// financialObjectStorageMeter reads apid's live object-storage estimate for
// the month. It reports no meter when the store keeps no estimates or apid
// has recorded none, for example where object storage is not configured.
func financialObjectStorageMeter(ctx context.Context, store state.Store, account string, start, end, asOf, now time.Time) (api.FinancialMeterCosts, bool, error) {
	estimates, ok := store.(state.FinancialObjectStorageEstimateStore)
	if !ok {
		return api.FinancialMeterCosts{}, false, nil
	}
	e, err := estimates.FinancialObjectStorageEstimate(ctx, account, start)
	if errors.Is(err, state.ErrNotFound) {
		return api.FinancialMeterCosts{}, false, nil
	}
	if err != nil {
		return api.FinancialMeterCosts{}, false, err
	}
	m, err := objectStorageMeterCosts(account, start, end, asOf, now, e)
	return m, err == nil, err
}

// objectStorageMeterCosts prices an estimate exactly as the usage endpoint
// and month close do (objectstorage.CalculateCharge). It has one account
// allocation: buckets carry no app or job attribution. The forecast scales
// each usage component by the month's length over the reported time and
// prices the projection with the same rates and allowance.
func objectStorageMeterCosts(account string, start, end, asOf, now time.Time, e state.FinancialObjectStorageEstimate) (api.FinancialMeterCosts, error) {
	meter := financial.MeterObjectStorage
	closed := !now.Before(end)
	fresh := e.Fresh && (closed || (!e.ObservedAt.After(now) && now.Sub(e.ObservedAt) <= api.FinancialObjectStorageEstimateFreshness))
	expected := int64(asOf.Sub(start) / time.Minute)
	reported := min(int64(e.UsageThrough.Sub(start)/time.Minute), expected)
	out := api.FinancialMeterCosts{Meter: meter, Coverage: api.FinancialMeterCoverage{Complete: true, Fresh: fresh, ExpectedMinutes: expected, CompleteMinutes: reported, Reasons: []string{}}, PriceContracts: []api.FinancialPriceContract{},
		Forecast: financial.Forecast{Method: "elapsed_time_run_rate_v1", AccountID: account, PeriodStart: start, PeriodEnd: end, CompleteThrough: e.UsageThrough, Meter: meter, Currency: e.Pricing.Currency}}
	if !fresh {
		out.Coverage.Reasons = append(out.Coverage.Reasons, "stale_evidence")
	}
	gross, err := objectstorage.CalculateCharge(e.Pricing, e.Usage, api.ObjectStorageAllowance{})
	if err != nil {
		return out, err
	}
	net, err := objectstorage.CalculateCharge(e.Pricing, e.Usage, e.Allowance)
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(fmt.Appendf(nil, "%+v|%+v", e.Pricing, e.Allowance))
	price := financial.Price{Version: "object-storage-v1-" + hex.EncodeToString(digest[:8]), Meter: meter, Currency: e.Pricing.Currency, Unit: "stored_byte_hours", UnitQuantity: 1, IncludedQuantity: e.Allowance.StoredByteHours}
	quantity := e.Usage.StoredByteHours
	allocation := financial.Allocation{Quantity: quantity, GrossMillicents: gross.TotalMillicents, AllowanceMillicents: gross.TotalMillicents - net.TotalMillicents, NetMillicents: net.TotalMillicents}
	out.Accrued = financial.ContractCosts{Meter: meter, Quantity: quantity, IncludedQuantity: e.Allowance.StoredByteHours, NetMillicents: net.TotalMillicents, AllowanceMethod: "object_storage_plan_allowance", Contracts: []financial.MeterCost{{
		Price: price, Quantity: quantity, IncludedQuantity: e.Allowance.StoredByteHours, GrossMillicents: gross.TotalMillicents,
		AllowanceMillicents: allocation.AllowanceMillicents, NetMillicents: net.TotalMillicents, AllocationMethod: "account", Allocations: []financial.Allocation{allocation},
	}}}
	out.Forecast.PriceVersion = price.Version
	elapsed := e.UsageThrough.Sub(start)
	switch {
	case closed:
		out.Forecast.Reason = "period_closed"
	case !fresh:
		out.Forecast.Reason = "stale_evidence"
	case elapsed < 24*time.Hour:
		out.Forecast.Reason = "insufficient_history"
	default:
		projected, err := projectObjectStorageUsage(e.Usage, end.Sub(start), elapsed)
		if err != nil {
			return out, err
		}
		grossProjected, err := objectstorage.CalculateCharge(e.Pricing, projected, api.ObjectStorageAllowance{})
		if err != nil {
			return out, err
		}
		netProjected, err := objectstorage.CalculateCharge(e.Pricing, projected, e.Allowance)
		if err != nil {
			return out, err
		}
		out.Forecast.Available = true
		out.Forecast.ProjectedQuantity = &projected.StoredByteHours
		out.Forecast.ProjectedNetMillicents = &netProjected.TotalMillicents
		out.Forecast.ProjectedGrossMillicents = &grossProjected.TotalMillicents
	}
	return out, nil
}

func projectObjectStorageUsage(u api.ObjectStorageUsage, period, elapsed time.Duration) (api.ObjectStorageUsage, error) {
	scale := func(v int64) (int64, error) {
		n := new(big.Int).Mul(big.NewInt(v), big.NewInt(int64(period)))
		n.Quo(n, big.NewInt(int64(elapsed)))
		if !n.IsInt64() {
			return 0, financial.ErrOverflow
		}
		return n.Int64(), nil
	}
	var out api.ObjectStorageUsage
	var err error
	if out.StoredByteHours, err = scale(u.StoredByteHours); err != nil {
		return out, err
	}
	if out.RequestCount, err = scale(u.RequestCount); err != nil {
		return out, err
	}
	out.EgressBytes, err = scale(u.EgressBytes)
	return out, err
}
