package flags

import (
	"fmt"
	"math"
	"slices"
)

// Guardrails protect production while an experiment runs (ADR
// 20261116190208 §1): each compares a per-request outcome rate of a variant
// with control, with customers as units, and is breached when the variant
// is worse than control by more than a tolerance.

// Guardrail names.
const (
	GuardrailServerErrorRate = "server_error_rate"
	GuardrailP95Latency      = "p95_latency"
)

// Guardrail statuses.
const (
	GuardrailOK               = "ok"
	GuardrailBreached         = "breached"
	GuardrailInsufficientData = "insufficient_data"
)

// RatioArm is one variant's per-unit outcome counts for a guardrail: Num
// outcomes over Den requests across Units customers, with the sums of
// squares and cross products the delta method needs.
type RatioArm struct {
	Variant string  `json:"variant"`
	Units   int64   `json:"units"`
	Num     float64 `json:"num"`
	Den     float64 `json:"den"`
	NumSq   float64 `json:"num_sq"`
	DenSq   float64 `json:"den_sq"`
	NumDen  float64 `json:"num_den"`
}

// Ratio is Num/Den, 0 with no requests.
func (a RatioArm) Ratio() float64 {
	if a.Den <= 0 {
		return 0
	}
	return a.Num / a.Den
}

// ratioVariance is the delta-method variance of Num/Den when units, not
// requests, are independent.
func (a RatioArm) ratioVariance() float64 {
	if a.Units < 2 || a.Den <= 0 {
		return 0
	}
	n := float64(a.Units)
	mx, my := a.Num/n, a.Den/n
	vx := (a.NumSq - n*mx*mx) / (n - 1)
	vy := (a.DenSq - n*my*my) / (n - 1)
	cxy := (a.NumDen - n*mx*my) / (n - 1)
	r := a.Ratio()
	return math.Max(0, (vx-2*r*cxy+r*r*vy)/(n*my*my))
}

// CompareRatio runs the mSPRT on the difference of two arms' ratios, with
// mixing standard deviation tau.
func CompareRatio(control, variant RatioArm, tau, alpha float64) (Comparison, error) {
	if control.Units < 2 || variant.Units < 2 {
		return Comparison{}, errNoVariance
	}
	v := control.ratioVariance() + variant.ratioVariance()
	// When every customer of an arm has the same rate (no errors at all, or
	// a variant failing every request) the delta-method variance is 0 and no
	// difference could ever be detected. The binomial variance of the
	// smoothed request-level rate bounds it from below.
	if floor := control.binomialFloor() + variant.binomialFloor(); v < floor {
		v = floor
	}
	return compareNormal(variant.Variant, control.Ratio(), variant.Ratio(), v, tau, alpha)
}

// binomialFloor is the variance of the arm's rate as if its requests were
// independent, with the rate smoothed to (Num+1)/(Den+2) so it is never 0.
func (a RatioArm) binomialFloor() float64 {
	if a.Den <= 0 {
		return 0
	}
	p := (a.Num + 1) / (a.Den + 2)
	return p * (1 - p) / (a.Den + 2)
}

// GuardrailInput is one guardrail's data and settings.
type GuardrailInput struct {
	Name        string
	Tolerance   float64
	Control     string
	Arms        []RatioArm
	Alpha       float64
	MinUnits    int64
	MinRequests float64
}

// GuardrailVariant is one variant's guardrail comparison.
type GuardrailVariant struct {
	Variant      string      `json:"variant"`
	ControlRate  float64     `json:"control_rate"`
	VariantRate  float64     `json:"variant_rate"`
	ControlUnits int64       `json:"control_units"`
	VariantUnits int64       `json:"variant_units"`
	Comparison   *Comparison `json:"comparison,omitempty"`
	Status       string      `json:"status"`
}

// GuardrailResult is a guardrail across every variant.
type GuardrailResult struct {
	Name      string             `json:"name"`
	Tolerance float64            `json:"tolerance"`
	Variants  []GuardrailVariant `json:"variants"`
	// Status is breached when any variant is breached, ok when every
	// variant is ok, and insufficient_data otherwise.
	Status string `json:"status"`
}

// EvaluateGuardrail compares every non-control arm with control. A variant
// breaches when the always-valid lower bound of (variant - control) is above
// the tolerance; the mixing standard deviation is the tolerance.
func EvaluateGuardrail(in GuardrailInput) GuardrailResult {
	out := GuardrailResult{Name: in.Name, Tolerance: in.Tolerance, Status: GuardrailOK}
	var control RatioArm
	for _, a := range in.Arms {
		if a.Variant == in.Control {
			control = a
		}
	}
	enough := func(a RatioArm) bool { return a.Units >= in.MinUnits && a.Den >= in.MinRequests }
	arms := slices.Clone(in.Arms)
	slices.SortFunc(arms, func(a, b RatioArm) int { return compareText(a.Variant, b.Variant) })
	insufficient := false
	for _, a := range arms {
		if a.Variant == in.Control {
			continue
		}
		g := GuardrailVariant{Variant: a.Variant, ControlRate: control.Ratio(), VariantRate: a.Ratio(),
			ControlUnits: control.Units, VariantUnits: a.Units, Status: GuardrailInsufficientData}
		if c, err := CompareRatio(control, a, in.Tolerance, in.Alpha); err == nil {
			g.Comparison = &c
			if enough(control) && enough(a) {
				g.Status = GuardrailOK
				if c.Low > in.Tolerance {
					g.Status = GuardrailBreached
				}
			}
		}
		switch g.Status {
		case GuardrailBreached:
			out.Status = GuardrailBreached
		case GuardrailInsufficientData:
			insufficient = true
		}
		out.Variants = append(out.Variants, g)
	}
	if out.Status != GuardrailBreached && (insufficient || len(out.Variants) == 0) {
		out.Status = GuardrailInsufficientData
	}
	return out
}

// Breach is the first breached variant of the results, in name order.
func Breach(results []GuardrailResult) (GuardrailResult, GuardrailVariant, bool) {
	for _, r := range results {
		for _, v := range r.Variants {
			if v.Status == GuardrailBreached {
				return r, v, true
			}
		}
	}
	return GuardrailResult{}, GuardrailVariant{}, false
}

func compareText(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// LatencyBucket maps a latency to its 5% log histogram bucket.
func LatencyBucket(ms int64) int {
	if ms <= 0 {
		return 0
	}
	return int(math.Ceil(math.Log(float64(ms)+1) / math.Log(1.05)))
}

// LatencyBucketUpper is the largest latency in a bucket.
func LatencyBucketUpper(bucket int) int64 {
	if bucket <= 0 {
		return 0
	}
	return int64(math.Floor(math.Pow(1.05, float64(bucket)) - 1))
}

// HistogramQuantile is the q quantile of a latency histogram (bucket to
// requests), as the upper bound of the bucket that holds it; 0 when empty.
func HistogramQuantile(h map[int]int64, q float64) int64 {
	var total int64
	buckets := make([]int, 0, len(h))
	for b, n := range h {
		if n > 0 {
			total += n
			buckets = append(buckets, b)
		}
	}
	if total == 0 {
		return 0
	}
	slices.Sort(buckets)
	rank := int64(math.Ceil(q * float64(total)))
	var seen int64
	for _, b := range buckets {
		seen += h[b]
		if seen >= rank {
			return LatencyBucketUpper(b)
		}
	}
	return LatencyBucketUpper(buckets[len(buckets)-1])
}

// Recommendation actions (ADR 20261116190208 §3).
const (
	RecommendRolledBack    = "rolled_back"
	RecommendFixAllocation = "fix_allocation"
	RecommendShipVariant   = "ship_variant"
	RecommendKeepControl   = "keep_control"
	RecommendKeepRunning   = "keep_running"
	RecommendDecide        = "decide"
)

// Recommendation says what to do with an experiment's results. Variant is
// the variant to ship for ship_variant.
type Recommendation struct {
	Action  string `json:"action"`
	Variant string `json:"variant,omitempty"`
	Summary string `json:"summary"`
}

// RecommendInput is what Recommend reads.
type RecommendInput struct {
	Results    Results
	Kind       string
	Running    bool
	StopReason string
	Guardrails []GuardrailResult
}

// Recommend turns results into one action. It never acts.
func Recommend(in RecommendInput) Recommendation {
	if in.StopReason == StopGuardrailBreach {
		return Recommendation{Action: RecommendRolledBack, Summary: "a guardrail stopped the experiment and sent every customer of the rule to control; fix the variant before testing it again"}
	}
	if in.Results.SampleRatioError {
		return Recommendation{Action: RecommendFixAllocation, Summary: "customers are not split as configured (sample ratio mismatch); results are not trustworthy until the allocation is fixed"}
	}
	var best *VariantResult
	worse := 0
	significant := 0
	for i := range in.Results.Variants {
		v := &in.Results.Variants[i]
		if v.Status != ExperimentSignificant || v.Comparison == nil {
			continue
		}
		significant++
		if v.Comparison.Lift < 0 {
			worse++
			continue
		}
		if best == nil || v.Comparison.Lift > best.Comparison.Lift {
			best = v
		}
	}
	breached := false
	for _, g := range in.Guardrails {
		for _, gv := range g.Variants {
			if best != nil && gv.Variant == best.Variant && gv.Status == GuardrailBreached {
				breached = true
			}
		}
	}
	switch {
	case best != nil && !breached:
		return Recommendation{Action: RecommendShipVariant, Variant: best.Variant,
			Summary: fmt.Sprintf("%s is better than %s: %s; ship it to every customer of the rule", best.Variant, in.Results.Control.Variant, describeLift(in.Kind, *best.Comparison))}
	case significant > 0 && worse == significant:
		return Recommendation{Action: RecommendKeepControl, Summary: fmt.Sprintf("every significant variant is worse than %s; stop the experiment and keep control", in.Results.Control.Variant)}
	case in.Running:
		return Recommendation{Action: RecommendKeepRunning, Summary: "no variant differs from control yet; keep the experiment running (results stay valid however often you read them)"}
	}
	return Recommendation{Action: RecommendDecide, Summary: "the experiment stopped without a significant difference; keep control or ship a variant on other grounds"}
}

func describeLift(kind string, c Comparison) string {
	if kind == ExperimentMetricMean {
		return fmt.Sprintf("%+.4g per customer (%+.1f%%), interval [%+.4g, %+.4g]", c.Lift, 100*c.RelativeLift, c.Low, c.High)
	}
	return fmt.Sprintf("%+.2f percentage points (%+.1f%% relative), interval [%+.2f, %+.2f] points", 100*c.Lift, 100*c.RelativeLift, 100*c.Low, 100*c.High)
}

// GuardrailSetting is one guardrail's owner setting: Disabled turns it
// off, MaxIncrease is its tolerance.
type GuardrailSetting struct {
	Disabled    bool    `json:"disabled,omitempty"`
	MaxIncrease float64 `json:"max_increase,omitempty"`
}

// GuardrailSettings are an experiment's guardrails.
type GuardrailSettings struct {
	ServerErrorRate GuardrailSetting `json:"server_error_rate"`
	P95Latency      GuardrailSetting `json:"p95_latency"`
}

// Stop reasons.
const (
	StopManual          = "manual"
	StopGuardrailBreach = "guardrail_breach"
	StopShipped         = "shipped"
)

// ServeVariant returns cfg with the experiment's rule serving variant to
// every customer it matches. It reports false, leaving cfg unchanged, when
// the rule is gone or no longer allocates by weight.
func ServeVariant(cfg Config, flagKey, ruleID, variant string) (Config, bool) {
	i := slices.IndexFunc(cfg.Flags, func(f Flag) bool { return f.Key == flagKey })
	if i < 0 {
		return cfg, false
	}
	f := cfg.Flags[i]
	if !slices.ContainsFunc(f.Variants, func(v FlagVariant) bool { return v.Key == variant }) {
		return cfg, false
	}
	j := slices.IndexFunc(f.Rules, func(r Rule) bool { return r.ID == ruleID })
	if j < 0 || f.Rules[j].Value != nil {
		return cfg, false
	}
	out := cfg
	out.Flags = slices.Clone(cfg.Flags)
	f.Rules = slices.Clone(f.Rules)
	f.Rules[j].Value = variant
	// A rule serving one variant carries no experiment (ADR 20261119173702 §2).
	f.Rules[j].Experiment = nil
	out.Flags[i] = f
	return out, true
}
