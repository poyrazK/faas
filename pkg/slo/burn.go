package slo

import (
	"context"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// MultiWindowBurn is the ADR-082 alert shape applied to a customer SLO: the
// 1h burn rate, with the 6h burn rescaled onto the same threshold, so one
// "above 14.4" comparison means both 1h > 14.4× and 6h > 6×. A window with
// no requests burns nothing. A short spike alone, or a slow leak the 1h
// window has already recovered from, stays below the threshold.
func MultiWindowBurn(ctx context.Context, prom PromQL, def state.SLO) (float64, error) {
	burn := func(rng string) (float64, error) {
		good, total, err := Counts(ctx, prom, def, rng, time.Time{})
		if err != nil {
			return 0, err
		}
		b, _ := BurnRate(good, total, def.ObjectiveBP)
		return b, nil
	}
	short, err := burn(appmetrics.SLOBurnRateShortWindow)
	if err != nil {
		return 0, err
	}
	long, err := burn(appmetrics.SLOBurnRateLongWindow)
	if err != nil {
		return 0, err
	}
	return math.Min(short, long*appmetrics.SLOBurnRateShortLimit/appmetrics.SLOBurnRateLongLimit), nil
}
