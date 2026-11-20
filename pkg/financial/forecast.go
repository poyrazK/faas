package financial

import (
	"math/big"
	"time"
)

// Forecast is a transparent quantity run-rate projection priced against the
// same historical meter contract as accrued cost. It is never an invoice or an
// admission decision. Unavailable projections carry no invented zero estimate.
type Forecast struct {
	Method                 string    `json:"method"`
	Available              bool      `json:"available"`
	Reason                 string    `json:"reason,omitempty"`
	AccountID              string    `json:"account_id"`
	PeriodStart            time.Time `json:"period_start"`
	PeriodEnd              time.Time `json:"period_end"`
	CompleteThrough        time.Time `json:"complete_through"`
	PriceVersion           string    `json:"price_version"`
	Meter                  string    `json:"meter"`
	Currency               string    `json:"currency"`
	ProjectedQuantity      *int64    `json:"projected_quantity,omitempty"`
	ProjectedNetMillicents *int64    `json:"projected_net_millicents,omitempty"`
	// ProjectedGrossMillicents is set only for account-level meters whose
	// charge is not one unit price, such as object storage
	// (ADR-20261020160000).
	ProjectedGrossMillicents *int64 `json:"projected_gross_millicents,omitempty"`
}

// ForecastMeter requires an uninterrupted coverage interval from period start,
// at least one observed day, and a caller-provided freshness verdict. Daily
// aggregates with gaps must not be passed as complete coverage.
func ForecastMeter(accountID string, start, end, completeThrough time.Time, quantity int64, price Price, coverageComplete, fresh bool) (Forecast, error) {
	var covered time.Duration
	if coverageComplete {
		covered = completeThrough.Sub(start)
	}
	return ForecastMeterOverCovered(accountID, start, end, completeThrough, covered, quantity, price, fresh)
}

// ForecastMeterOverCovered projects the run rate over the covered time
// only: quantity was observed in covered of the elapsed period, so a few
// unsampled minutes neither stop the forecast nor understate it. covered
// equal to the elapsed time is ForecastMeter's uninterrupted coverage;
// zero means coverage too incomplete to project. The caller decides how
// much of the elapsed time must be covered.
func ForecastMeterOverCovered(accountID string, start, end, completeThrough time.Time, covered time.Duration, quantity int64, price Price, fresh bool) (Forecast, error) {
	out := Forecast{Method: "elapsed_time_run_rate_v1", AccountID: accountID, PeriodStart: start.UTC(), PeriodEnd: end.UTC(), CompleteThrough: completeThrough.UTC(), PriceVersion: price.Version, Meter: price.Meter, Currency: price.Currency}
	if accountID == "" || !start.Before(end) || end.Sub(start) > 32*24*time.Hour || quantity < 0 || completeThrough.Before(start) || completeThrough.After(end) {
		return out, ErrInvalid
	}
	if _, err := CostMeter(accountID, start, end, price, nil); err != nil {
		return out, err
	}
	if covered <= 0 || covered > completeThrough.Sub(start) {
		out.Reason = "incomplete_coverage"
		return out, nil
	}
	if !fresh {
		out.Reason = "stale_evidence"
		return out, nil
	}
	elapsed := covered
	if covered < completeThrough.Sub(start) {
		out.Method = "covered_time_run_rate_v1"
	}
	if elapsed < 24*time.Hour {
		out.Reason = "insufficient_history"
		return out, nil
	}
	n := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(int64(end.Sub(start))))
	n.Quo(n, big.NewInt(int64(elapsed)))
	if !n.IsInt64() {
		return out, ErrOverflow
	}
	projected := n.Int64()
	net, err := priceQuantity(big.NewInt(max(0, projected-price.IncludedQuantity)), price)
	if err != nil {
		return out, err
	}
	out.Available = true
	out.ProjectedQuantity = &projected
	out.ProjectedNetMillicents = &net
	return out, nil
}
