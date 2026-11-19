package flags

import (
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Funnel and retention metrics and CUPED (ADR 20261119102741 §1–2).

// Funnel and retention metric kinds. Both are binary per customer, like
// conversion.
const (
	ExperimentMetricFunnel    = "funnel"
	ExperimentMetricRetention = "retention"
)

// Retention periods.
const (
	PeriodDay  = "day"
	PeriodWeek = "week"
)

// BinaryMetric reports whether kind counts converted customers.
func BinaryMetric(kind string) bool {
	return kind == "" || kind == ExperimentMetricConversion || kind == ExperimentMetricFunnel || kind == ExperimentMetricRetention
}

// AnalyticsMetric reports whether kind reads product analytics events.
func AnalyticsMetric(kind string) bool {
	return kind == ExperimentMetricFunnel || kind == ExperimentMetricRetention
}

// MetricStep is one funnel step or a retention return event: an event name
// and the property values its events must carry.
type MetricStep struct {
	Event string            `json:"event"`
	Where map[string]string `json:"where,omitempty"`
}

// MetricDefinition holds the parts of a funnel or retention metric.
type MetricDefinition struct {
	Steps        []MetricStep `json:"steps,omitempty"`
	Window       string       `json:"window,omitempty"`
	Return       *MetricStep  `json:"return,omitempty"`
	Period       string       `json:"period,omitempty"`
	PeriodNumber int          `json:"period_number,omitempty"`
}

// WindowDuration is the funnel window; zero when unset or invalid.
func (d MetricDefinition) WindowDuration() time.Duration {
	w, err := time.ParseDuration(d.Window)
	if err != nil {
		return 0
	}
	return w
}

// PeriodDuration is one retention period.
func (d MetricDefinition) PeriodDuration() time.Duration {
	if d.Period == PeriodWeek {
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// Horizon is how long after first exposure a funnel or retention unit's
// outcome is final; zero for other kinds (ADR 20261119102741 §1).
func (d MetricDefinition) Horizon(kind string) time.Duration {
	switch kind {
	case ExperimentMetricFunnel:
		return d.WindowDuration()
	case ExperimentMetricRetention:
		return time.Duration(d.PeriodNumber+1) * d.PeriodDuration()
	}
	return 0
}

// StartOffset is when after first exposure a unit's conversion window
// opens: N periods for retention, else zero.
func (d MetricDefinition) StartOffset(kind string) time.Duration {
	if kind == ExperimentMetricRetention {
		return time.Duration(d.PeriodNumber) * d.PeriodDuration()
	}
	return 0
}

// PrimaryEvent is the event CUPED reads for a binary metric's covariate and
// the experiment's metric_event: the funnel's first step or the retention
// return event.
func (d MetricDefinition) PrimaryEvent(kind string) MetricStep {
	switch kind {
	case ExperimentMetricFunnel:
		if len(d.Steps) > 0 {
			return d.Steps[0]
		}
	case ExperimentMetricRetention:
		if d.Return != nil {
			return *d.Return
		}
	}
	return MetricStep{}
}

func validMetricStep(s MetricStep, validKey func(string) bool) error {
	if !ValidEventName(s.Event) {
		return fmt.Errorf("invalid event name %q", s.Event)
	}
	if len(s.Where) > api.AnalyticsEventMaxProperties {
		return fmt.Errorf("a step takes at most %d property filters", api.AnalyticsEventMaxProperties)
	}
	for k, v := range s.Where {
		if !validKey(k) || len(v) > api.AnalyticsPropertyValueMaxBytes {
			return fmt.Errorf("invalid property filter %q", k)
		}
	}
	return nil
}

// ValidateMetricDefinition checks the definition a kind needs and that other
// kinds carry none. validKey checks a property key.
func ValidateMetricDefinition(kind string, d MetricDefinition, validKey func(string) bool) error {
	switch kind {
	case ExperimentMetricFunnel:
		if len(d.Steps) < api.AnalyticsFunnelMinSteps || len(d.Steps) > api.AnalyticsFunnelMaxSteps {
			return fmt.Errorf("a funnel metric takes %d to %d steps", api.AnalyticsFunnelMinSteps, api.AnalyticsFunnelMaxSteps)
		}
		for _, s := range d.Steps {
			if err := validMetricStep(s, validKey); err != nil {
				return err
			}
		}
		w := d.WindowDuration()
		if w < api.AnalyticsFunnelMinWindow || w > api.AnalyticsFunnelMaxWindow {
			return fmt.Errorf("a funnel window is %s to %s", api.AnalyticsFunnelMinWindow, api.AnalyticsFunnelMaxWindow)
		}
		if d.Return != nil || d.Period != "" || d.PeriodNumber != 0 {
			return fmt.Errorf("a funnel metric takes no retention fields")
		}
	case ExperimentMetricRetention:
		if d.Return == nil {
			return fmt.Errorf("a retention metric needs a return event")
		}
		if err := validMetricStep(*d.Return, validKey); err != nil {
			return err
		}
		if d.Period != PeriodDay && d.Period != PeriodWeek {
			return fmt.Errorf("a retention period is day or week")
		}
		if d.PeriodNumber < 1 || d.PeriodNumber > api.ExperimentRetentionMaxPeriod {
			return fmt.Errorf("a retention period number is 1 to %d", api.ExperimentRetentionMaxPeriod)
		}
		if len(d.Steps) > 0 || d.Window != "" {
			return fmt.Errorf("a retention metric takes no funnel fields")
		}
	default:
		if len(d.Steps) > 0 || d.Window != "" || d.Return != nil || d.Period != "" || d.PeriodNumber != 0 {
			return fmt.Errorf("%s metrics take no funnel or retention fields", kind)
		}
	}
	return nil
}

// CupedArm is one variant's sums for CUPED: Y is the unit's metric (0 or 1,
// or its winsorized value) and X its pre-period covariate.
type CupedArm struct {
	Variant string  `json:"variant"`
	N       int64   `json:"n"`
	SumY    float64 `json:"sum_y"`
	SumY2   float64 `json:"sum_y2"`
	SumX    float64 `json:"sum_x"`
	SumX2   float64 `json:"sum_x2"`
	SumXY   float64 `json:"sum_xy"`
}

// CupedSummary reports whether CUPED adjusted the results.
type CupedSummary struct {
	Applied bool    `json:"applied"`
	Theta   float64 `json:"theta,omitempty"`
	// VarianceReduction is the share of the metric's variance the
	// covariate explains (ρ²).
	VarianceReduction float64 `json:"variance_reduction,omitempty"`
	// Reason says why CUPED did not apply.
	Reason string `json:"reason,omitempty"`
}

// CUPED reasons.
const (
	CupedOff        = "off"
	CupedPending    = "pre_period_pending"
	CupedNoVariance = "no_pre_period_variance"
	CupedNoData     = "no_pre_period_data"
	// CupedUnsupported is a metric or unit CUPED does not adjust: ratio and
	// percentile metrics and subject units (ADR 20261119173702).
	CupedUnsupported = "not_supported"
)

type cupedTotals struct{ n, y, y2, x, x2, xy float64 }

func sumCuped(arms []CupedArm) cupedTotals {
	var t cupedTotals
	for _, a := range arms {
		t.n += float64(a.N)
		t.y += a.SumY
		t.y2 += a.SumY2
		t.x += a.SumX
		t.x2 += a.SumX2
		t.xy += a.SumXY
	}
	return t
}

// CupedTheta is cov(X, Y) / var(X) over every unit and ρ²; ok is false when
// var(X) or var(Y) is zero.
func CupedTheta(arms []CupedArm) (theta, rho2, meanX float64, ok bool) {
	t := sumCuped(arms)
	if t.n < 2 {
		return 0, 0, 0, false
	}
	meanX = t.x / t.n
	meanY := t.y / t.n
	varX := t.x2/t.n - meanX*meanX
	varY := t.y2/t.n - meanY*meanY
	cov := t.xy/t.n - meanX*meanY
	if !(varX > 1e-12) || !(varY > 1e-12) {
		return 0, 0, meanX, false
	}
	theta = cov / varX
	rho2 = math.Min(1, cov*cov/(varX*varY))
	return theta, rho2, meanX, true
}

// adjusted is the arm's CUPED mean and the variance of one unit's adjusted
// value.
func (a CupedArm) adjusted(theta, meanX float64) (mean, variance float64) {
	n := float64(a.N)
	my, mx := a.SumY/n, a.SumX/n
	vy := math.Max(0, (a.SumY2-n*my*my)/(n-1))
	vx := math.Max(0, (a.SumX2-n*mx*mx)/(n-1))
	cxy := (a.SumXY - n*mx*my) / (n - 1)
	return my - theta*(mx-meanX), math.Max(0, vy-2*theta*cxy+theta*theta*vx)
}

// CompareCuped runs the mSPRT on the CUPED-adjusted difference of means.
// tau is the mixing standard deviation, chosen by the caller as for the
// unadjusted comparison.
func CompareCuped(control, variant CupedArm, theta, meanX, tau, alpha float64) (Comparison, error) {
	if control.N < 2 || variant.N < 2 {
		return Comparison{}, errNoVariance
	}
	mc, vc := control.adjusted(theta, meanX)
	mv, vv := variant.adjusted(theta, meanX)
	return compareNormal(variant.Variant, mc, mv, vc/float64(control.N)+vv/float64(variant.N), tau, alpha)
}
