package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Funnel and retention experiment metrics and CUPED (ADR 20261119102741
// §1–2): the request, the response and the reconciler's analytics steps.

// productAnalyticsOn reports whether an account's plan keeps analytics
// events and the product_analytics platform flag is on for it.
func (s *server) productAnalyticsOn(accountID, plan string) bool {
	return api.Plan(plan).AnalyticsRetentionDays() > 0 &&
		s.platformFlagFor(platformflags.ProductAnalytics, platformflags.Subject{AccountID: accountID, Plan: plan})
}

// flagExperimentMetric is a validated metric of a create request.
type flagExperimentMetric struct {
	kind, event string
	def         flags.MetricDefinition
}

func metricStep(s api.InsightStepRequest) flags.MetricStep {
	return flags.MetricStep{Event: s.Event, Where: s.Where}
}

func apiStep(s flags.MetricStep) api.InsightStepRequest {
	return api.InsightStepRequest{Event: s.Event, Where: s.Where}
}

// flagExperimentMetricFrom validates a create request's metric. Funnel and
// retention metrics need product analytics on the account.
func (s *server) flagExperimentMetricFrom(req api.FlagExperimentCreateRequest, acct state.Account) (flagExperimentMetric, *api.Problem) {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid experiment", detail)
	}
	m := flagExperimentMetric{kind: req.MetricKind, event: req.MetricEvent}
	if m.kind == "" {
		m.kind = flags.ExperimentMetricConversion
	}
	if req.Funnel != nil {
		for _, st := range req.Funnel.Steps {
			m.def.Steps = append(m.def.Steps, metricStep(st))
		}
		m.def.Window = req.Funnel.Window
	}
	if req.Retention != nil {
		ret := metricStep(req.Retention.Return)
		m.def.Return, m.def.Period, m.def.PeriodNumber = &ret, req.Retention.Period, req.Retention.PeriodNumber
	}
	if err := flags.ValidateMetricDefinition(m.kind, m.def, insights.ValidPropertyKey); err != nil {
		return m, invalid(err.Error())
	}
	if !flags.AnalyticsMetric(m.kind) {
		if !flags.ValidEventName(m.event) {
			return m, invalid("metric_event is a conversion event name: ^[a-z][a-z0-9_.-]{0,63}$")
		}
		return m, nil
	}
	if m.event != "" {
		return m, invalid("funnel and retention metrics take no metric_event")
	}
	if !s.productAnalyticsOn(acct.ID, string(acct.Plan)) {
		return m, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Product analytics unavailable",
			"funnel and retention metrics read product analytics events, which this account's plan does not keep")
	}
	if m.kind == flags.ExperimentMetricFunnel {
		m.event = m.def.Steps[len(m.def.Steps)-1].Event
	} else {
		m.event = m.def.Return.Event
	}
	return m, nil
}

// flagExperimentMetricResponse fills a response's funnel or retention.
func flagExperimentMetricResponse(out *api.FlagExperiment, x state.FlagExperiment) {
	d := x.MetricDefinition
	switch x.MetricKind {
	case flags.ExperimentMetricFunnel:
		f := &api.FlagExperimentFunnel{Window: d.Window}
		for _, st := range d.Steps {
			f.Steps = append(f.Steps, apiStep(st))
		}
		out.Funnel = f
	case flags.ExperimentMetricRetention:
		if d.Return != nil {
			out.Retention = &api.FlagExperimentRetention{Return: apiStep(*d.Return), Period: d.Period, PeriodNumber: d.PeriodNumber}
		}
	}
	out.Cuped = x.Cuped
}

// flagExperimentCupedInput is what CUPED needs from the store.
type flagExperimentCupedInput struct {
	arms   []flags.CupedArm
	reason string
}

// loadFlagExperimentCuped reads CUPED sums when CUPED is on, analytics is
// on and every counted unit has its pre-period value.
func loadFlagExperimentCuped(ctx context.Context, store state.FlagExperimentStore, x state.FlagExperiment, analyticsOn bool) (flagExperimentCupedInput, error) {
	as, ok := store.(state.FlagExperimentAnalyticsStore)
	switch {
	case !x.Cuped || !ok:
		return flagExperimentCupedInput{reason: flags.CupedOff}, nil
	case !cupedSupported(x):
		return flagExperimentCupedInput{reason: flags.CupedUnsupported}, nil
	case !analyticsOn:
		return flagExperimentCupedInput{reason: flags.CupedNoData}, nil
	}
	arms, pending, err := as.FlagExperimentCupedArms(ctx, x, api.FlagExperimentMinUnitsPerArm)
	if err != nil {
		return flagExperimentCupedInput{}, err
	}
	if pending > 0 {
		return flagExperimentCupedInput{reason: flags.CupedPending}, nil
	}
	return flagExperimentCupedInput{arms: arms}, nil
}

// cupedSupported reports whether CUPED applies: not to subject units,
// whose pre-period is not per customer, nor to ratio or percentile metrics
// (ADR 20261119173702 §1, §3).
func cupedSupported(x state.FlagExperiment) bool {
	return !x.SubjectUnits() && !customMetric(x.MetricKind)
}

func cupedResponse(c flags.CupedSummary) api.FlagExperimentCuped {
	return api.FlagExperimentCuped{Applied: c.Applied, PreDays: api.ExperimentCupedPreDays, Theta: c.Theta,
		VarianceReduction: c.VarianceReduction, Reason: c.Reason}
}

func comparisonResponse(c *flags.Comparison) *api.FlagExperimentComparison {
	if c == nil {
		return nil
	}
	return &api.FlagExperimentComparison{Lift: c.Lift, RelativeLift: c.RelativeLift, Low: c.Low, High: c.High, PValue: c.PValue}
}

// conversionGrace covers events that arrive up to a day late
// (FlagEventMaxAge) when deciding which deadlines can still change.
const conversionGrace = api.FlagEventMaxAge + time.Hour

// markAnalyticsConversions marks funnel or retention conversions of units
// whose deadline is recent enough to still change, at most
// ExperimentAnalyticsUnitsPerPass.
func (r *flagExperimentReconciler) markAnalyticsConversions(ctx context.Context, as state.FlagExperimentAnalyticsStore, x state.FlagExperiment, now time.Time) error {
	store, ok := r.server.store.(state.AnalyticsStore)
	if !ok {
		return nil
	}
	after := ""
	for checked := 0; checked < api.ExperimentAnalyticsUnitsPerPass; {
		units, err := as.FlagExperimentConversionCandidates(ctx, x, after, now, now.Add(-conversionGrace), min(2000, api.ExperimentAnalyticsUnitsPerPass-checked))
		if err != nil || len(units) == 0 {
			return err
		}
		found, err := insights.ExperimentConversions(ctx, store, x, units)
		if err != nil {
			return err
		}
		if err := as.MarkFlagExperimentUnitConversions(ctx, x.ID, found); err != nil {
			return err
		}
		checked += len(units)
		after = units[len(units)-1].CustomerID
	}
	return nil
}

// fillPreValues gives units without a CUPED value their pre-period value.
func (r *flagExperimentReconciler) fillPreValues(ctx context.Context, as state.FlagExperimentAnalyticsStore, x state.FlagExperiment) error {
	cohorts, ok := r.server.store.(state.AnalyticsCohortStore)
	if !ok {
		return nil
	}
	units, err := as.FlagExperimentUnitsWithoutPreValue(ctx, x.ID, api.ExperimentAnalyticsUnitsPerPass)
	if err != nil || len(units) == 0 {
		return err
	}
	ids := make([]string, 0, len(units))
	for _, u := range units {
		ids = append(ids, u.CustomerID)
	}
	for start := 0; start < len(ids); start += 2000 {
		batch := ids[start:min(start+2000, len(ids))]
		values, err := insights.PreValues(ctx, cohorts, x, batch)
		if err != nil {
			return err
		}
		if err := as.SetFlagExperimentPreValues(ctx, x.ID, values); err != nil {
			return err
		}
	}
	return nil
}

// cohortRefreshBatch bounds the cohorts one pass recomputes.
const cohortRefreshBatch = 20

// refreshCohorts recomputes cohorts whose refresh was requested or is an
// hour old (ADR 20261119102741 §3). A cohort of an account without product
// analytics keeps its members and records why.
func (r *flagExperimentReconciler) refreshCohorts(ctx context.Context, now time.Time) {
	cohorts, ok := r.server.store.(state.AnalyticsCohortStore)
	store, ok2 := r.server.store.(state.AnalyticsStore)
	if !ok || !ok2 {
		return
	}
	due, err := cohorts.DueAnalyticsCohorts(ctx, now.Add(-api.AnalyticsCohortRefreshInterval), cohortRefreshBatch)
	if err != nil {
		r.warn(ctx, "analytics cohorts: list due failed", err)
		return
	}
	for _, c := range due {
		if ctx.Err() != nil {
			return
		}
		if !r.server.productAnalyticsOn(c.AccountID, c.Plan) {
			if err := cohorts.FailAnalyticsCohortRefresh(ctx, c.ID, now, "product analytics is off for this account"); err != nil {
				r.warn(ctx, "analytics cohorts: record failure failed", err, "cohort", c.ID)
			}
			continue
		}
		cCtx, cancel := context.WithTimeout(ctx, api.AnalyticsQueryTimeout)
		members, truncated, err := insights.CohortMembers(cCtx, store, cohorts, c, now, api.Plan(c.Plan).AnalyticsCohortMaxMembers())
		if err == nil {
			err = cohorts.ReplaceAnalyticsCohortMembers(cCtx, c, members, truncated, now)
		}
		cancel()
		if err != nil {
			r.warn(ctx, "analytics cohorts: refresh failed", err, "cohort", c.ID)
			if ferr := cohorts.FailAnalyticsCohortRefresh(ctx, c.ID, now, "refresh failed; the previous members stay in place"); ferr != nil {
				r.warn(ctx, "analytics cohorts: record failure failed", ferr, "cohort", c.ID)
			}
		}
	}
}

// analyticsSteps runs the analytics parts of a pass for an experiment.
func (r *flagExperimentReconciler) analyticsSteps(ctx context.Context, x state.FlagExperiment, now time.Time) error {
	as, ok := r.store.(state.FlagExperimentAnalyticsStore)
	if !ok || !r.server.productAnalyticsOn(x.AccountID, x.Plan) {
		return nil
	}
	if x.UsesAnalyticsConversions() {
		if err := r.markAnalyticsConversions(ctx, as, x, now); err != nil {
			return err
		}
	}
	if x.Cuped && cupedSupported(x) {
		return r.fillPreValues(ctx, as, x)
	}
	return nil
}
