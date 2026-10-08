// pkg/meter/savings.go — scale-to-zero savings estimate for one app.
//
// The customer-facing /v1/apps/{slug}/savings endpoint compares what
// an app actually billed in a window against an always-on
// counterfactual: the same billable RAM held RUNNING for every second
// of the window, at the app's configured floor (max(min_instances, 1)).
// The difference is the RAM the platform reclaimed by parking the app.
//
// The estimate is deliberately conservative:
//
//   - The counterfactual uses api.BillableRAMMB(app RAM) without
//     companion sidecars, while ActualMBSeconds comes from
//     usage_minutes and already includes sidecar MB. Sidecars can
//     only shrink the reported saving, never inflate it.
//   - The counterfactual is one floor of instances, not the observed
//     peak. A burst that ran several instances at once can make the
//     actual figure exceed the counterfactual; the saving clamps to
//     zero rather than going negative.
//   - Money is valued at the marginal GB-hour price passed by the
//     caller (the plan overage rate today), in integer millicents.
//
// This helper is plan-agnostic and does no I/O; the handler supplies
// the window, the app shape, the actual usage, and the price.

package meter

import "time"

// SavingsInput is everything ScaleToZeroSavings needs. Window bounds
// are a half-open [Since, Until) range; the caller clamps Since to the
// app's creation time so a brand-new app is not credited with savings
// from before it existed.
type SavingsInput struct {
	Since, Until time.Time
	// BillableRAMMB is the per-instance billable RAM (plan RAM + the
	// per-VM overhead), i.e. api.BillableRAMMB(app.RAMMB).
	BillableRAMMB int
	// FloorInstances is the app's configured minimum instance count.
	// Values below 1 are treated as 1: an always-on API keeps at least
	// one instance resident.
	FloorInstances int
	// ActualMBSeconds is the billed RAM consumption in the window, from
	// AppWindowSummary.MBSeconds.
	ActualMBSeconds int64
	// PriceMillicentsPerGBHour values one GB-RAM-hour.
	PriceMillicentsPerGBHour int64
}

// SavingsEstimate is the result of ScaleToZeroSavings. MB-second
// fields are exact integers; GB-hour fields are rounded to 6 decimal
// places like AppWindowSummary.GBHours; money is integer millicents.
type SavingsEstimate struct {
	WindowSeconds      int64
	AlwaysOnMBSeconds  int64
	ActualMBSeconds    int64
	SavedMBSeconds     int64
	AlwaysOnGBHours    float64
	ActualGBHours      float64
	SavedGBHours       float64
	AlwaysOnMillicents int64
	ActualMillicents   int64
	SavedMillicents    int64
	// ParkedRatio is SavedMBSeconds / AlwaysOnMBSeconds in [0, 1]: the
	// share of always-on RAM time the app did not consume.
	ParkedRatio float64
}

// mbSecondsPerGBHour converts MB-seconds to GB-hours (1024 MB × 3600 s).
const mbSecondsPerGBHour = 1024 * 3600

// ScaleToZeroSavings computes the always-on counterfactual and the
// saving against the actual billed usage. An empty or inverted window,
// or a non-positive RAM size, yields a zero estimate that still echoes
// the actual usage.
func ScaleToZeroSavings(in SavingsInput) SavingsEstimate {
	out := SavingsEstimate{ActualMBSeconds: max(in.ActualMBSeconds, 0)}
	if in.Until.After(in.Since) && in.BillableRAMMB > 0 {
		out.WindowSeconds = int64(in.Until.Sub(in.Since) / time.Second)
		floor := int64(max(in.FloorInstances, 1))
		out.AlwaysOnMBSeconds = int64(in.BillableRAMMB) * floor * out.WindowSeconds
	}
	out.SavedMBSeconds = max(out.AlwaysOnMBSeconds-out.ActualMBSeconds, 0)

	out.AlwaysOnGBHours = roundGBHours(out.AlwaysOnMBSeconds)
	out.ActualGBHours = roundGBHours(out.ActualMBSeconds)
	out.SavedGBHours = roundGBHours(out.SavedMBSeconds)

	out.AlwaysOnMillicents = valueMillicents(out.AlwaysOnMBSeconds, in.PriceMillicentsPerGBHour)
	out.ActualMillicents = valueMillicents(out.ActualMBSeconds, in.PriceMillicentsPerGBHour)
	out.SavedMillicents = valueMillicents(out.SavedMBSeconds, in.PriceMillicentsPerGBHour)

	if out.AlwaysOnMBSeconds > 0 {
		out.ParkedRatio = float64(int64(float64(out.SavedMBSeconds)/float64(out.AlwaysOnMBSeconds)*1e6+0.5)) / 1e6
	}
	return out
}

func roundGBHours(mbSeconds int64) float64 {
	return float64(int64(GBHours(mbSeconds)*1e6+0.5)) / 1e6
}

// valueMillicents prices MB-seconds at a per-GB-hour rate, rounding half
// up in integer arithmetic so money never passes through a float.
func valueMillicents(mbSeconds, priceMillicentsPerGBHour int64) int64 {
	if mbSeconds <= 0 || priceMillicentsPerGBHour <= 0 {
		return 0
	}
	return (mbSeconds*priceMillicentsPerGBHour + mbSecondsPerGBHour/2) / mbSecondsPerGBHour
}
