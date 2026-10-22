package apidserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	platformFlagRampInterval = time.Minute
	platformFlagRampTimeout  = 30 * time.Second
	// platformFlagRampActor is the audit actor of automatic ramp writes; the
	// runtime-config row itself records no actor.
	platformFlagRampActor = "system:platform-flags-ramp"
)

// startPlatformFlagRamps runs the automatic ramp worker (ADR 20261021080640)
// once a minute. Every replica runs it: a stage moves only through a write
// that expects the flag's current version, so exactly one replica advances
// it and the others see a conflict and stop.
func startPlatformFlagRamps(ctx context.Context, s *server, log *slog.Logger) {
	go func() {
		ticker := time.NewTicker(platformFlagRampInterval)
		defer ticker.Stop()
		for {
			s.advancePlatformFlagRamps(ctx, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	if log != nil {
		log.Info("platform_flags.ramp_worker_started", "interval", platformFlagRampInterval)
	}
}

// advancePlatformFlagRamps judges every ramp of every flag that has health
// routes. One flag's failure does not stop the others.
func (s *server) advancePlatformFlagRamps(ctx context.Context, now time.Time) {
	for _, spec := range platformflags.Catalog() {
		if len(spec.HealthRoutes) == 0 {
			continue
		}
		flagCtx, cancel := context.WithTimeout(ctx, platformFlagRampTimeout)
		err := s.advancePlatformFlagRamp(flagCtx, spec, now)
		cancel()
		if err != nil && s.log != nil {
			s.log.Warn("platform_flags.ramp_failed", "flag", spec.Key, "err", err)
		}
	}
}

// rampEvent is one ramp's move, audited after the write lands.
type rampEvent struct {
	rule, outcome, reason string
	from, to              int
}

// advancePlatformFlagRamp moves the ramps of one flag whose current version
// has held for their interval. All moves of one flag land in one write.
func (s *server) advancePlatformFlagRamp(ctx context.Context, spec platformflags.Spec, now time.Time) error {
	key := platformflags.ConfigKey(spec.Key)
	row, err := s.store.GetRuntimeConfig(ctx, key, state.RuntimeConfigScopeGlobal, "")
	if errors.Is(err, state.ErrRuntimeConfigNotFound) {
		return nil // only an operator-written definition can carry a ramp
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", key, err)
	}
	def, err := platformflags.ParseDefinition(row.DesiredValue)
	if err != nil {
		return fmt.Errorf("parse %s: %w", key, err)
	}
	since := s.platformFlagVersionTime(ctx, row)
	var events []rampEvent
	for i, rule := range def.Rules {
		if rule.Ramp == nil || rule.Ramp.Paused || rule.Rollout == nil {
			continue
		}
		window := time.Duration(rule.Ramp.IntervalSeconds) * time.Second
		if now.Sub(since) < window {
			continue
		}
		next, outcome, reason := platformflags.RampStep(rule, s.platformFlagRampHealth(ctx, spec, window))
		if outcome == platformflags.RampHold || outcome == platformflags.RampComplete {
			if s.log != nil {
				s.log.Debug("platform_flags.ramp_held", "flag", spec.Key, "rule", rule.ID, "outcome", outcome, "reason", reason)
			}
			continue
		}
		def.Rules[i] = next
		events = append(events, rampEvent{rule: rule.ID, outcome: outcome, reason: reason, from: *rule.Rollout, to: *next.Rollout})
	}
	if len(events) == 0 {
		return nil
	}
	return s.writePlatformFlagRamp(ctx, spec, row, def, events)
}

// writePlatformFlagRamp stores the moved ramps with the version they were
// judged against. A conflict means an operator or another replica changed
// the flag first; the next pass judges the new version.
func (s *server) writePlatformFlagRamp(ctx context.Context, spec platformflags.Spec, row state.RuntimeConfig, def platformflags.Definition, events []rampEvent) error {
	reasons := make([]string, 0, len(events))
	for _, e := range events {
		reasons = append(reasons, fmt.Sprintf("ramp %s %s: %s", e.rule, e.outcome, e.reason))
	}
	expected := row.Version
	updated, err := s.store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: row.Key, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: def.Marshal(),
		ApplyMode: state.RuntimeConfigApplyHot, Reason: truncateReason(strings.Join(reasons, "; ")), ExpectedVersion: &expected,
	})
	if errors.Is(err, state.ErrRuntimeConfigConflict) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", row.Key, err)
	}
	if err := s.applyHotRuntimeConfig(ctx, updated); err != nil && !errors.Is(err, state.ErrRuntimeConfigConflict) && s.log != nil {
		s.log.Warn("platform_flags.ramp_apply_deferred", "flag", spec.Key, "version", updated.Version, "err", err)
	}
	_ = s.notif.Notify(ctx, db.NotifyRuntimeConfigChanged, row.Key)
	for _, e := range events {
		if s.audit != nil {
			s.audit.EmitAs(ctx, platformFlagRampActor, "platform_flag.ramp_"+e.outcome, nil, map[string]any{
				"flag": spec.Key, "rule": e.rule, "from": e.from, "to": e.to, "reason": e.reason, "version": updated.Version,
			})
		}
	}
	return nil
}

// platformFlagVersionTime is when the flag's current version was written,
// which is when its current stage began.
func (s *server) platformFlagVersionTime(ctx context.Context, row state.RuntimeConfig) time.Time {
	revision, err := s.store.GetRuntimeConfigRevision(ctx, row.Key, row.Scope, row.ScopeID, row.Version)
	if err == nil && !revision.CreatedAt.IsZero() {
		return revision.CreatedAt
	}
	return row.UpdatedAt
}

// platformFlagRampHealth measures the flag's health routes over window from
// apid's own request counters; code="err" counts 5xx responses. Without
// Prometheus, or when a query fails, health is unknown and ramps hold.
func (s *server) platformFlagRampHealth(ctx context.Context, spec platformflags.Spec, window time.Duration) platformflags.RampHealth {
	client := s.promqlClient
	if client == nil {
		return platformflags.RampHealth{}
	}
	routes := make([]string, 0, len(spec.HealthRoutes))
	for _, route := range spec.HealthRoutes {
		routes = append(routes, regexp.QuoteMeta(route))
	}
	selector := fmt.Sprintf("route=~%q", strings.Join(routes, "|"))
	rng := fmt.Sprintf("%ds", int(window.Seconds()))
	total, ok := rampCount(ctx, client, fmt.Sprintf("sum(increase(apid_request_total{%s}[%s]))", selector, rng))
	if !ok {
		return platformflags.RampHealth{}
	}
	failures, ok := rampCount(ctx, client, fmt.Sprintf(`sum(increase(apid_request_total{%s,code="err"}[%s]))`, selector, rng))
	if !ok {
		return platformflags.RampHealth{}
	}
	return platformflags.RampHealth{Known: true, Requests: total, Failures: failures}
}

// rampCount runs one counting query; no samples means no requests.
func rampCount(ctx context.Context, client *promql.Client, query string) (int64, bool) {
	value, err := client.QueryScalar(ctx, query)
	if errors.Is(err, promql.ErrNoData) {
		return 0, true
	}
	if err != nil || value < 0 {
		return 0, false
	}
	return int64(math.Round(value)), true
}

// truncateReason keeps a change reason within the runtime-config bound.
func truncateReason(reason string) string {
	if len(reason) <= 500 {
		return reason
	}
	return reason[:497] + "..."
}
