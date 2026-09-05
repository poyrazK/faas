package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// RolloutStore is the durable slice required by the safety controller. It is
// intentionally narrower than state.Store so the controller can be tested
// with a small fake and so adding this worker does not widen unrelated seams.
type RolloutStore interface {
	ListRuntimeConfigs(ctx context.Context, scope state.RuntimeConfigScope, scopeID string) ([]state.RuntimeConfig, error)
	ListRuntimeConfigAcks(ctx context.Context, key string, scope state.RuntimeConfigScope, scopeID string) ([]state.RuntimeConfigAck, error)
	ListRuntimeConfigRevisions(ctx context.Context, key string, scope state.RuntimeConfigScope, scopeID string, limit int) ([]state.RuntimeConfigRevision, error)
	UpsertRuntimeConfig(ctx context.Context, update state.RuntimeConfigUpdate) (state.RuntimeConfig, error)
	MarkRuntimeConfigApplied(ctx context.Context, key string, scope state.RuntimeConfigScope, scopeID string, version int64, effectiveValue json.RawMessage, applyErr string) error
}

// RolloutStateStore is implemented by the production PgStore and MemStore.
// Keeping it optional lets the controller continue to run against a database
// during the migration window; the value remains safe even if the lifecycle
// column has not been deployed yet.
type RolloutStateStore interface {
	MarkRuntimeConfigRolloutState(ctx context.Context, key string, scope state.RuntimeConfigScope, scopeID string, version int64, rolloutState state.RuntimeConfigRolloutState, lastError string) error
}

// Notifier is the low-latency wake-up path for daemons after an automatic
// rollback. The durable row remains authoritative if notification delivery
// fails.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// AuditFunc emits an operator audit event. The controller never includes the
// configuration value in the payload, only the key, version, and reason.
type AuditFunc func(ctx context.Context, kind string, fields map[string]any)

// RolloutController observes applied daemon canaries and either leaves them
// alone (healthy/unavailable evidence) or restores the latest stable revision
// when a health threshold or daemon acknowledgement failure is observed.
type RolloutController struct {
	Store    RolloutStore
	Health   PrometheusHealthProvider
	Policy   HealthPolicy
	Notifier Notifier
	Audit    AuditFunc
	Log      *slog.Logger
	Interval time.Duration
	MinAge   time.Duration
	// Steps is the ordered rollout ladder used for opt-in automatic
	// promotion. A nil or invalid ladder falls back to DefaultRolloutSteps.
	Steps []int
	Now   func() time.Time
}

type RolloutStats struct {
	Observed   int
	Promoted   int
	RolledBack int
	Paused     int
	Unhealthy  int
	Errors     int
}

// DefaultRolloutSteps keeps each automatic step small near the start of a
// rollout while still reaching the full fleet in a bounded number of passes.
var DefaultRolloutSteps = []int{1, 5, 25, 50, 100}

// NewRolloutController builds the background safety worker. The worker is
// deliberately inert when no Prometheus client is configured, except that a
// failed daemon acknowledgement can still trigger a rollback.
func NewRolloutController(store RolloutStore, health PrometheusHealthProvider, notifier Notifier, audit AuditFunc, log *slog.Logger) *RolloutController {
	if log == nil {
		log = slog.Default()
	}
	return &RolloutController{
		Store: store, Health: health, Policy: health.Policy, Notifier: notifier,
		Audit: audit, Log: log, Interval: 30 * time.Second, MinAge: time.Minute,
		Steps: append([]int(nil), DefaultRolloutSteps...),
		Now:   time.Now,
	}
}

// Run keeps the worker alive until shutdown. RunOnce is public so callers and
// tests can drive one deterministic safety evaluation.
func (c *RolloutController) Run(ctx context.Context) error {
	if c == nil || c.Store == nil {
		return nil
	}
	interval := c.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if _, err := c.RunOnce(ctx); err != nil && ctx.Err() == nil {
		c.Log.Warn("runtime_config rollout safety pass failed", "err", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := c.RunOnce(ctx); err != nil && ctx.Err() == nil {
				c.Log.Warn("runtime_config rollout safety pass failed", "err", err)
			}
		}
	}
}

func (c *RolloutController) RunOnce(ctx context.Context) (RolloutStats, error) {
	if c == nil || c.Store == nil {
		return RolloutStats{}, nil
	}
	rows, err := c.Store.ListRuntimeConfigs(ctx, "", "")
	if err != nil {
		return RolloutStats{}, fmt.Errorf("list runtime config canaries: %w", err)
	}
	stats := RolloutStats{}
	var firstErr error
	for _, row := range rows {
		if !c.isCandidate(row) {
			continue
		}
		stats.Observed++
		if c.minAge() > 0 && c.now().Sub(row.UpdatedAt) < c.minAge() {
			continue
		}
		reason, ready, err := c.failureReason(ctx, row)
		if err != nil {
			stats.Errors++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !ready {
			continue
		}
		if reason == "" {
			if !row.AutoPromote {
				continue
			}
			promoted, err := c.promote(ctx, row)
			if err != nil {
				stats.Errors++
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if promoted {
				stats.Promoted++
			}
			continue
		}
		stats.Unhealthy++
		rolledBack, err := c.rollback(ctx, row, reason)
		if err != nil {
			stats.Errors++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if rolledBack {
			stats.RolledBack++
		} else {
			stats.Paused++
		}
	}
	return stats, firstErr
}

func (c *RolloutController) isCandidate(row state.RuntimeConfig) bool {
	return row.Scope == state.RuntimeConfigScopeDaemon &&
		row.ApplyMode == state.RuntimeConfigApplyHot &&
		row.Status == state.RuntimeConfigApplied &&
		row.RolloutPercent > 0 && row.RolloutPercent < 100 &&
		(row.RolloutState == state.RuntimeConfigRolloutCanary || row.RolloutState == state.RuntimeConfigRolloutPromoting)
}

// failureReason returns (reason, ready). ready is false when there is not
// enough evidence yet or Prometheus is unavailable; the controller must not
// mutate a canary on missing telemetry.
func (c *RolloutController) failureReason(ctx context.Context, row state.RuntimeConfig) (string, bool, error) {
	acks, err := c.Store.ListRuntimeConfigAcks(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil {
		return "", false, fmt.Errorf("list runtime config acknowledgements for %s v%d: %w", row.Key, row.Version, err)
	}
	applied := 0
	for _, ack := range acks {
		if ack.Version != row.Version {
			continue
		}
		if ack.Status == state.RuntimeConfigAckFailed {
			if ack.Error == "" {
				return fmt.Sprintf("daemon %s rejected version %d", ack.Consumer, row.Version), true, nil
			}
			return fmt.Sprintf("daemon %s rejected version %d: %s", ack.Consumer, row.Version, ack.Error), true, nil
		}
		if ack.Status == state.RuntimeConfigAckApplied {
			applied++
		}
	}
	if applied == 0 {
		return "", false, nil
	}
	snapshot, err := c.Health.Snapshot(ctx)
	if err != nil {
		c.Log.Warn("runtime_config rollout health unavailable; leaving canary live", "key", row.Key, "scope_id", row.ScopeID, "version", row.Version, "err", err)
		return "", false, nil
	}
	policy := c.policy().normalized()
	if snapshot.Requests < policy.MinRequests {
		// An idle canary has not produced enough evidence to promote or
		// roll back. Leave it live and wait for the next observation window.
		return "", false, nil
	}
	if policyFailure := policy.Evaluate(snapshot); policyFailure != nil {
		// A policy breach is expected evidence for rollback, not a controller
		// execution error. Return it as the durable operator-facing reason.
		return policyFailure.Error(), true, nil //nolint:nilerr // policy breaches are rollback reasons, not controller errors
	}
	return "", true, nil
}

// promote advances an opt-in canary to the next fixed ladder step. The
// write is version-protected so an operator edit wins cleanly over a stale
// controller pass. Intermediate steps are marked promoting while they wait
// for their next observation window; 100% is terminal stable state.
func (c *RolloutController) promote(ctx context.Context, row state.RuntimeConfig) (bool, error) {
	next := c.nextRolloutPercent(row.RolloutPercent)
	if next <= row.RolloutPercent {
		return false, nil
	}
	expected := row.Version
	percent := next
	autoPromote := next < 100
	reason := fmt.Sprintf("automatic promotion of v%d from %d%% to %d%%", row.Version, row.RolloutPercent, next)
	updated, err := c.Store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID,
		DesiredValue: row.DesiredValue, RolloutPercent: &percent,
		AutoPromote: autoPromote, ApplyMode: state.RuntimeConfigApplyHot,
		Reason: reason, ExpectedVersion: &expected,
	})
	if err != nil {
		if errors.Is(err, state.ErrRuntimeConfigConflict) {
			return false, nil
		}
		return false, fmt.Errorf("write automatic promotion for %s v%d: %w", row.Key, row.Version, err)
	}
	if err := c.Store.MarkRuntimeConfigApplied(ctx, updated.Key, updated.Scope, updated.ScopeID, updated.Version, updated.DesiredValue, ""); err != nil {
		if errors.Is(err, state.ErrRuntimeConfigConflict) {
			return false, nil
		}
		return false, fmt.Errorf("apply automatic promotion for %s v%d: %w", row.Key, updated.Version, err)
	}
	if stateStore, ok := c.Store.(RolloutStateStore); ok {
		rolloutState := state.RuntimeConfigRolloutPromoting
		if next == 100 {
			rolloutState = state.RuntimeConfigRolloutStable
		}
		if err := stateStore.MarkRuntimeConfigRolloutState(ctx, updated.Key, updated.Scope, updated.ScopeID, updated.Version, rolloutState, ""); err != nil && !errors.Is(err, state.ErrRuntimeConfigConflict) {
			return false, fmt.Errorf("mark automatic promotion for %s v%d: %w", row.Key, updated.Version, err)
		}
	}
	if c.Notifier != nil {
		_ = c.Notifier.Notify(ctx, db.NotifyRuntimeConfigChanged, row.Key)
	}
	if c.Audit != nil {
		c.Audit(ctx, "operator.runtime_config_auto_promote", map[string]any{
			"key": row.Key, "scope": row.Scope, "scope_id": row.ScopeID,
			"from_version": row.Version, "to_version": updated.Version,
			"from_percent": row.RolloutPercent, "to_percent": next,
		})
	}
	c.Log.Info("runtime_config canary automatically promoted", "key", row.Key, "scope_id", row.ScopeID, "from_percent", row.RolloutPercent, "to_percent", next, "version", updated.Version)
	return true, nil
}

func (c *RolloutController) nextRolloutPercent(current int) int {
	steps := c.Steps
	if !validRolloutSteps(steps) {
		steps = DefaultRolloutSteps
	}
	for _, step := range steps {
		if step > current {
			return step
		}
	}
	return current
}

func validRolloutSteps(steps []int) bool {
	if len(steps) == 0 {
		return false
	}
	previous := 0
	for _, step := range steps {
		if step <= previous || step < 1 || step > 100 {
			return false
		}
		previous = step
	}
	return steps[len(steps)-1] == 100
}

// rollback restores the newest older revision that was fleet-stable
// (rollout_percent=100). If no stable revision exists, it pauses the canary
// rather than guessing a value or widening the blast radius.
func (c *RolloutController) rollback(ctx context.Context, row state.RuntimeConfig, reason string) (bool, error) {
	revisions, err := c.Store.ListRuntimeConfigRevisions(ctx, row.Key, row.Scope, row.ScopeID, 200)
	if err != nil {
		return false, fmt.Errorf("list rollback revisions for %s v%d: %w", row.Key, row.Version, err)
	}
	var previous *state.RuntimeConfigRevision
	for i := range revisions {
		revision := &revisions[i]
		if revision.Version >= row.Version {
			continue
		}
		if previous == nil || revision.Version > previous.Version {
			previous = revision
		}
		if revision.RolloutPercent >= 100 {
			previous = revision
			break
		}
	}
	if previous == nil {
		return false, c.pause(ctx, row, "no stable revision exists; "+reason)
	}
	percent := previous.RolloutPercent
	expected := row.Version
	rollbackReason := fmt.Sprintf("automatic rollback of v%d to v%d: %s", row.Version, previous.Version, reason)
	updated, err := c.Store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID,
		DesiredValue: previous.NewValue, RolloutPercent: &percent, AutoPromote: previous.AutoPromote,
		ApplyMode: state.RuntimeConfigApplyHot, Reason: rollbackReason,
		ExpectedVersion: &expected,
	})
	if err != nil {
		if errors.Is(err, state.ErrRuntimeConfigConflict) {
			return false, nil
		}
		return false, fmt.Errorf("write automatic rollback for %s v%d: %w", row.Key, row.Version, err)
	}
	if err := c.Store.MarkRuntimeConfigApplied(ctx, updated.Key, updated.Scope, updated.ScopeID, updated.Version, updated.DesiredValue, ""); err != nil {
		if errors.Is(err, state.ErrRuntimeConfigConflict) {
			return false, nil
		}
		return false, fmt.Errorf("apply automatic rollback for %s v%d: %w", row.Key, updated.Version, err)
	}
	if stateStore, ok := c.Store.(RolloutStateStore); ok {
		if err := stateStore.MarkRuntimeConfigRolloutState(ctx, updated.Key, updated.Scope, updated.ScopeID, updated.Version, state.RuntimeConfigRolloutRolledBack, rollbackReason); err != nil && !errors.Is(err, state.ErrRuntimeConfigConflict) {
			return false, fmt.Errorf("mark automatic rollback for %s v%d: %w", row.Key, updated.Version, err)
		}
	}
	if c.Notifier != nil {
		_ = c.Notifier.Notify(ctx, db.NotifyRuntimeConfigChanged, row.Key)
	}
	if c.Audit != nil {
		c.Audit(ctx, "operator.runtime_config_auto_rollback", map[string]any{
			"key": row.Key, "scope": row.Scope, "scope_id": row.ScopeID,
			"from_version": row.Version, "to_version": previous.Version,
			"rollout_percent": percent, "reason": reason,
		})
	}
	c.Log.Warn("runtime_config canary automatically rolled back", "key", row.Key, "scope_id", row.ScopeID, "from_version", row.Version, "to_version", previous.Version, "reason", reason)
	return true, nil
}

func (c *RolloutController) pause(ctx context.Context, row state.RuntimeConfig, reason string) error {
	stateStore, ok := c.Store.(RolloutStateStore)
	if !ok {
		return fmt.Errorf("runtime config canary %s v%d cannot be rolled back: %s", row.Key, row.Version, reason)
	}
	if err := stateStore.MarkRuntimeConfigRolloutState(ctx, row.Key, row.Scope, row.ScopeID, row.Version, state.RuntimeConfigRolloutPaused, reason); err != nil {
		return fmt.Errorf("pause runtime config canary %s v%d: %w", row.Key, row.Version, err)
	}
	if c.Audit != nil {
		c.Audit(ctx, "operator.runtime_config_rollout_paused", map[string]any{
			"key": row.Key, "scope": row.Scope, "scope_id": row.ScopeID,
			"version": row.Version, "reason": reason,
		})
	}
	c.Log.Warn("runtime_config canary paused without stable rollback target", "key", row.Key, "scope_id", row.ScopeID, "version", row.Version, "reason", reason)
	return nil
}

func (c *RolloutController) policy() HealthPolicy {
	policy := c.Policy
	if policy == (HealthPolicy{}) {
		policy = DefaultHealthPolicy
	}
	return policy
}

func (c *RolloutController) minAge() time.Duration {
	if c.MinAge < 0 {
		return 0
	}
	if c.MinAge == 0 {
		return time.Minute
	}
	return c.MinAge
}

func (c *RolloutController) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}
