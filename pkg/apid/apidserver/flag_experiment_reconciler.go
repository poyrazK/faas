package apidserver

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// The experiment reconciler (ADR 20261115163113 §3) adds units from new
// request telemetry, marks conversions and keeps each variant's running
// minimum p-value, so results cover the whole experiment while telemetry and
// daily events age out on the plan's schedule. It also sweeps daily
// conversion rows past each plan's request telemetry retention.
const (
	flagExperimentInterval = 5 * time.Minute
	flagExperimentBatch    = 50
	flagExperimentTimeout  = time.Minute
	// flagExperimentSettle leaves the newest telemetry minutes, which the
	// gateway may still be rolling up, for the next pass.
	flagExperimentSettle = 2 * time.Minute
	// flagExperimentWindow and flagExperimentWindowsPerPass bound how much
	// telemetry one experiment reads per pass.
	flagExperimentWindow         = time.Hour
	flagExperimentWindowsPerPass = 24
	flagConversionSweepAccounts  = 500
)

type flagExperimentReconciler struct {
	server  *server
	store   state.FlagExperimentStore
	events  state.FlagEventStore
	log     *slog.Logger
	afterID string
}

func startFlagExperimentReconciler(ctx context.Context, s *server, log *slog.Logger) {
	if s == nil || s.store == nil {
		return
	}
	store, ok := s.store.(state.FlagExperimentStore)
	if !ok {
		return
	}
	events, _ := s.store.(state.FlagEventStore)
	r := &flagExperimentReconciler{server: s, store: store, events: events, log: log}
	go func() {
		ticker := time.NewTicker(flagExperimentInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.runPass(ctx, time.Now())
			}
		}
	}()
}

func (r *flagExperimentReconciler) warn(ctx context.Context, msg string, err error, args ...any) {
	if r.log != nil && !errors.Is(err, context.Canceled) {
		r.log.WarnContext(ctx, msg, append([]any{"err", err}, args...)...)
	}
}

func (r *flagExperimentReconciler) runPass(ctx context.Context, now time.Time) {
	// Retention holds whatever the flags say: raw analytics events past
	// their plan's retention go even while analytics or flags are off.
	r.sweepAnalytics(ctx, now)
	r.refreshCohorts(ctx, now)
	if !r.server.platformFlagOn(platformflags.CustomerFlags) {
		return
	}
	if r.events != nil {
		if _, err := r.events.SweepFlagConversions(ctx, now, func(plan string) int { return api.Plan(plan).DebugTelemetryRetentionDays() }, flagConversionSweepAccounts); err != nil {
			r.warn(ctx, "flag experiments: conversion sweep failed", err)
		}
	}
	running, err := r.store.ListRunningFlagExperiments(ctx, r.afterID, flagExperimentBatch)
	if err != nil {
		r.warn(ctx, "flag experiments: list running failed", err)
		return
	}
	if len(running) < flagExperimentBatch {
		r.afterID = ""
	} else {
		r.afterID = running[len(running)-1].ID
	}
	for _, x := range running {
		if ctx.Err() != nil {
			return
		}
		xCtx, cancel := context.WithTimeout(ctx, flagExperimentTimeout)
		if err := r.advance(xCtx, x, now); err != nil {
			r.warn(ctx, "flag experiments: advance failed", err, "experiment", x.ID)
		}
		if err := r.guard(xCtx, x, now); err != nil {
			r.warn(ctx, "flag experiments: guardrail check failed", err, "experiment", x.ID)
		}
		if err := r.banditStep(xCtx, x, now); err != nil {
			r.warn(ctx, "flag experiments: bandit step failed", err, "experiment", x.ID)
		}
		if err := r.notify(xCtx, x, now); err != nil {
			r.warn(ctx, "flag experiments: notification failed", err, "experiment", x.ID)
		}
		cancel()
	}
}

// analyticsSweepBatches bounds the delete statements one pass runs per plan.
const analyticsSweepBatches = 20

// sweepAnalytics deletes product analytics events past each plan's
// analytics retention (ADR 20261117084505 §2).
func (r *flagExperimentReconciler) sweepAnalytics(ctx context.Context, now time.Time) {
	store, ok := r.server.store.(state.AnalyticsStore)
	if !ok {
		return
	}
	retention := map[string]int{}
	for _, p := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		retention[string(p)] = p.AnalyticsRetentionDays()
	}
	if _, err := store.SweepAnalyticsEvents(ctx, now, retention, api.AnalyticsSweepBatch, analyticsSweepBatches); err != nil {
		r.warn(ctx, "product analytics: retention sweep failed", err)
	}
}

// advance reads request telemetry after the experiment's cursor, at most
// flagExperimentWindowsPerPass windows, never before the plan's telemetry
// retention, then stores the cursor and the running minimum p-values.
func (r *flagExperimentReconciler) advance(ctx context.Context, x state.FlagExperiment, now time.Time) error {
	until := now.Add(-flagExperimentSettle)
	from := x.TelemetryCursor
	if earliest := now.AddDate(0, 0, -api.Plan(x.Plan).DebugTelemetryRetentionDays()); from.Before(earliest) {
		from = earliest
	}
	if x.SubjectUnits() {
		// Subject units come from SDK exposures, not request telemetry
		// (ADR 20261119173702 §1); the cursor still bounds conversions.
		from = until
	}
	for i := 0; i < flagExperimentWindowsPerPass && from.Before(until); i++ {
		end := from.Add(flagExperimentWindow)
		if end.After(until) {
			end = until
		}
		read, err := r.store.AdvanceFlagExperimentUnits(ctx, x, from, end, api.FlagExperimentMaxUnitsPerPass)
		if err != nil {
			return err
		}
		if read >= api.FlagExperimentMaxUnitsPerPass && r.log != nil {
			r.log.WarnContext(ctx, "flag experiments: exposure window truncated", "experiment", x.ID, "from", from, "until", end)
		}
		from = end
	}
	if from.Before(x.TelemetryCursor) {
		from = x.TelemetryCursor
	}
	if x.MetricKind == flags.ExperimentMetricMean || customMetric(x.MetricKind) {
		if err := r.foldValues(ctx, &x, now, from); err != nil {
			return err
		}
	}
	// Funnel and retention units count once their deadline is before the
	// new cursor (ADR 20261119102741 §1).
	x.TelemetryCursor = from
	if err := r.analyticsSteps(ctx, x, now); err != nil {
		r.warn(ctx, "flag experiments: analytics metrics failed", err, "experiment", x.ID)
	}
	arms, _, err := r.store.FlagExperimentArms(ctx, x, api.FlagExperimentMinUnitsPerArm)
	if err != nil {
		return err
	}
	if x.MetricKind == flags.ExperimentMetricPercentile {
		// Bootstrap p-values are fixed-horizon: no running minimum.
		return r.store.SaveFlagExperimentProgress(ctx, x.ID, from, x.MinPValues, x.MinCupedPValues)
	}
	cuped, err := loadFlagExperimentCuped(ctx, r.store, x, r.server.productAnalyticsOn(x.AccountID, x.Plan))
	if err != nil {
		return err
	}
	extras, err := loadFlagExperimentExtras(ctx, r.store, x)
	if err != nil {
		return err
	}
	minP, minCuped := cloneFloatMap(x.MinPValues), cloneFloatMap(x.MinCupedPValues)
	keepMin := func(m map[string]float64, variant string, c *flags.Comparison) {
		if c == nil {
			return
		}
		if prev, ok := m[variant]; !ok || c.PValue < prev {
			m[variant] = c.PValue
		}
	}
	for _, v := range flagExperimentResultsFor(x, arms, cuped, extras).Variants {
		if v.Unadjusted != nil {
			// CUPED applied: Comparison is adjusted.
			keepMin(minP, v.Variant, v.Unadjusted)
			keepMin(minCuped, v.Variant, v.Comparison)
			continue
		}
		keepMin(minP, v.Variant, v.Comparison)
	}
	return r.store.SaveFlagExperimentProgress(ctx, x.ID, from, minP, minCuped)
}

func cloneFloatMap(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// guard fixes the latency threshold and checks the guardrails on the units
// already recorded. It runs even when a pass failed to read new telemetry,
// so a slow telemetry read never delays a rollback.
func (r *flagExperimentReconciler) guard(ctx context.Context, x state.FlagExperiment, now time.Time) error {
	if err := r.fixLatencyThreshold(ctx, &x); err != nil {
		return err
	}
	return r.checkGuardrails(ctx, x, now)
}

// foldValues adds closed days of the metric's values to the units: days
// before yesterday, whose events can no longer arrive (FlagEventMaxAge),
// and before the telemetry cursor's day, so every unit exposed on a folded
// day already exists (ADR 20261116190208 §2).
func (r *flagExperimentReconciler) foldValues(ctx context.Context, x *state.FlagExperiment, now, cursor time.Time) error {
	through := utcMidnight(now).AddDate(0, 0, -2)
	if c := utcMidnight(cursor).AddDate(0, 0, -1); c.Before(through) {
		through = c
	}
	if x.ValueThroughDay != nil && !through.After(*x.ValueThroughDay) {
		return nil
	}
	if x.StartedAt != nil && through.Before(utcMidnight(*x.StartedAt)) {
		return nil
	}
	if err := r.store.FoldFlagExperimentValues(ctx, *x, through); err != nil {
		return err
	}
	x.ValueThroughDay = &through
	return nil
}

func utcMidnight(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// fixLatencyThreshold fixes control's p95 latency as the latency threshold
// once control has enough requests.
func (r *flagExperimentReconciler) fixLatencyThreshold(ctx context.Context, x *state.FlagExperiment) error {
	if x.LatencyThresholdMS != nil || x.Guardrails.P95Latency.Disabled {
		return nil
	}
	hist, err := r.store.FlagExperimentLatency(ctx, x.ID)
	if err != nil {
		return err
	}
	var n int64
	for _, c := range hist[x.Control] {
		n += c
	}
	if n < api.FlagExperimentGuardrailMinRequests {
		return nil
	}
	ms := int(flags.HistogramQuantile(hist[x.Control], 0.95))
	if err := r.store.SetFlagExperimentLatencyThreshold(ctx, x.ID, ms); err != nil {
		return err
	}
	x.LatencyThresholdMS = &ms
	return nil
}

// checkGuardrails stops a breached experiment and sends every customer of
// its rule to control (ADR 20261116190208 §1).
func (r *flagExperimentReconciler) checkGuardrails(ctx context.Context, x state.FlagExperiment, now time.Time) error {
	errs, slow, err := r.store.FlagExperimentGuardrailArms(ctx, x.ID)
	if err != nil {
		return err
	}
	g, v, breached := flags.Breach(flagExperimentGuardrails(x, errs, slow))
	if !breached {
		return nil
	}
	version, err := r.server.serveFlagExperimentVariant(ctx, x, x.Control, flagExperimentGuardrailActor)
	rolledBack := err == nil
	if err != nil && !errors.Is(err, errFlagExperimentRuleChanged) {
		return err
	}
	breach := breachRecord(g, v, version, rolledBack)
	if _, err := r.store.StopFlagExperiment(ctx, x.EnvironmentID, x.Key, now, flags.StopGuardrailBreach, breach); err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	if r.server.audit != nil {
		r.server.audit.EmitAs(ctx, flagExperimentGuardrailActor, "flags.experiment_guardrail_breached", &x.AccountID, map[string]any{
			"project_id": x.ProjectID, "environment_id": x.EnvironmentID, "experiment": x.Key, "flag": x.FlagKey,
			"rule_id": x.RuleID, "control": x.Control, "breach": breach})
	}
	if r.log != nil {
		r.log.WarnContext(ctx, "flag experiments: guardrail breached", "experiment", x.ID, "guardrail", g.Name,
			"variant", v.Variant, "rolled_back", rolledBack, "config_version", version)
	}
	return nil
}
