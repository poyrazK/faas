package apidserver

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
		cancel()
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
	arms, _, err := r.store.FlagExperimentArms(ctx, x.ID)
	if err != nil {
		return err
	}
	minP := map[string]float64{}
	for k, v := range x.MinPValues {
		minP[k] = v
	}
	for _, v := range flagExperimentResults(x, arms).Variants {
		if v.Comparison == nil {
			continue
		}
		if prev, ok := minP[v.Variant]; !ok || v.Comparison.PValue < prev {
			minP[v.Variant] = v.Comparison.PValue
		}
	}
	if from.Before(x.TelemetryCursor) {
		from = x.TelemetryCursor
	}
	return r.store.SaveFlagExperimentProgress(ctx, x.ID, from, minP)
}
