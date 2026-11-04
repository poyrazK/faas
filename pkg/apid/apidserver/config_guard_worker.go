// adr: 1451 — apid judges config guards and restores failed config changes.
package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/configguard"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// configGuardActor is the actor recorded on a guard's restore revision.
const configGuardActor = "gregale:config-guard"

// configGuardAbandonAfter closes a guard no write landed in (the write's
// process died between begin and finish).
const configGuardAbandonAfter = 5 * time.Minute

// configGuardSweep evaluates every watching guard once. One failing guard
// does not stop the others.
func (s *server) configGuardSweep(ctx context.Context, now time.Time) error {
	s.configGuardSweepMu.Lock()
	defer s.configGuardSweepMu.Unlock()
	store, ok := s.store.(state.ConfigRevisionStore)
	if !ok {
		return nil
	}
	telemetry := s.configGuardTelemetry
	if telemetry == nil {
		telemetry, _ = s.store.(state.ConfigGuardTelemetryStore)
	}
	guards, err := store.ListWatchingConfigGuards(state.WithConfigGuardCursor(ctx, s.configGuardCursor), api.ConfigGuardBatchSize)
	if err != nil {
		return err
	}
	if len(guards) == 0 && s.configGuardCursor.ID != "" {
		s.configGuardCursor = state.ConfigGuardCursor{}
		guards, err = store.ListWatchingConfigGuards(ctx, api.ConfigGuardBatchSize)
		if err != nil {
			return err
		}
	}
	for _, guard := range guards {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.configGuardCursor = state.ConfigGuardCursor{ChangedAt: guard.LastChangeAt, ID: guard.ID}
		if err := s.evaluateConfigGuard(ctx, store, telemetry, guard, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Warn("config guard: evaluate", "app", guard.AppID, "scope", guard.Scope, "guard", guard.ID, "err", err)
		}
	}
	return nil
}

// evaluateConfigGuard opens the window on the first new-config traffic,
// restores (or records the regression) when that traffic fails, and closes
// the guard clean once the window and the telemetry grace have passed.
func (s *server) evaluateConfigGuard(ctx context.Context, store state.ConfigRevisionStore, telemetry state.ConfigGuardTelemetryStore, g state.ConfigGuard, now time.Time) error {
	if g.HeadRevision == g.BaseRevision {
		if now.Sub(g.LastChangeAt) >= configGuardAbandonAfter {
			return store.AbandonConfigChange(ctx, g.ID, now)
		}
		return nil
	}
	sample := state.ConfigGuardSample{}
	if telemetry != nil {
		until := now
		if g.WindowEndsAt != nil && g.WindowEndsAt.Before(now) {
			// Telemetry is minute-bucketed: the bucket holding the window's
			// end still belongs to the window.
			until = g.WindowEndsAt.Truncate(time.Minute).Add(time.Minute)
		}
		var err error
		sample, err = telemetry.ConfigGuardTelemetry(ctx, state.ConfigGuardTelemetryQuery{AppID: g.AppID, Scope: g.Scope,
			OpenedAt: g.OpenedAt, ChangedAt: g.LastChangeAt, BaselineFrom: g.OpenedAt.Add(-api.ConfigGuardBaseline), Until: until})
		if err != nil {
			return fmt.Errorf("read guard telemetry: %w", err)
		}
	}
	if sample.NewRequests == 0 {
		if now.Sub(g.LastChangeAt) >= api.ConfigGuardMaxWait {
			return s.closeConfigGuard(ctx, store, g, state.ConfigGuardNoTraffic, configguard.NoTraffic(), now)
		}
		return nil
	}
	if g.WindowEndsAt == nil {
		if _, err := store.OpenConfigGuardWindow(ctx, g.ID, g.LastChangeAt, now.Add(api.ConfigGuardWindowMinutes*time.Minute)); err != nil {
			return fmt.Errorf("open guard window: %w", err)
		}
	}
	decision, failed := configguard.Judge(sample)
	if failed {
		if !g.Restorable {
			decision.Summary += "; the change deleted or rotated a secret, so it is not restored automatically"
			return s.closeConfigGuard(ctx, store, g, state.ConfigGuardRegressed, decision, now)
		}
		return s.restoreConfigGuard(ctx, store, g, decision, now)
	}
	if g.WindowEndsAt != nil && now.After(g.WindowEndsAt.Add(api.RollbackOn5xxTelemetryGrace)) {
		return s.closeConfigGuard(ctx, store, g, state.ConfigGuardClean, decision, now)
	}
	return nil
}

func (s *server) closeConfigGuard(ctx context.Context, store state.ConfigRevisionStore, g state.ConfigGuard, status string, decision configguard.Decision, now time.Time) error {
	raw, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	closed, err := store.CloseConfigGuard(ctx, g.ID, status, raw, g.HeadRevision, now)
	if err != nil || !closed {
		return err
	}
	s.emitConfigGuardOutcome(ctx, g, status, decision)
	return nil
}

func (s *server) emitConfigGuardOutcome(ctx context.Context, g state.ConfigGuard, status string, decision configguard.Decision) {
	if status == state.ConfigGuardRegressed || status == state.ConfigGuardRestored {
		s.log.Warn("config guard: "+status, "app", g.AppID, "scope", g.Scope, "guard", g.ID, "rule", decision.Rule, "summary", decision.Summary)
		accountID := g.AccountID
		s.audit.Emit(ctx, "config.guard_"+status, &accountID, map[string]any{
			"app_id": g.AppID, "scope": g.Scope, "guard_id": g.ID, "base_revision": g.BaseRevision,
			"head_revision": g.HeadRevision, "rule": decision.Rule, "summary": decision.Summary,
			"new_requests": decision.NewRequests, "new_server_errors": decision.NewServerErrors,
			"restored_revision": decision.RestoredRevision,
		})
	}
}

// restoreConfigGuard returns the guard's own changes to its base revision:
// variables from the base revision, replaced secrets from their retained
// sealed rows, added secrets deleted. A guarded key changed outside the
// guard since makes the restore unsafe; the guard closes superseded.
func (s *server) restoreConfigGuard(ctx context.Context, store state.ConfigRevisionStore, g state.ConfigGuard, decision configguard.Decision, now time.Time) error {
	base, err := store.GetConfigRevision(ctx, g.AppID, g.Scope, g.BaseRevision)
	if err != nil {
		return fmt.Errorf("read base revision: %w", err)
	}
	head, err := store.GetConfigRevision(ctx, g.AppID, g.Scope, g.HeadRevision)
	if err != nil {
		return fmt.Errorf("read head revision: %w", err)
	}
	variables, secrets, err := store.ConfigState(ctx, g.AccountID, g.AppID, g.Scope)
	if err != nil {
		return fmt.Errorf("read current config: %w", err)
	}
	retained, err := store.ConfigGuardSecrets(ctx, g.ID)
	if err != nil {
		return fmt.Errorf("read retained secrets: %w", err)
	}
	writes, unchanged := configguard.GuardVariableRestore(base.Variables, head.Variables, variables)
	if !unchanged || !configguard.GuardSecretsUnchanged(retained, head.Secrets, secrets) {
		decision.Rule, decision.Summary = configguard.RuleChangedOutside, decision.Summary+"; a guarded key changed outside the guard, so nothing was restored"
		return s.closeConfigGuard(ctx, store, g, state.ConfigGuardSuperseded, decision, now)
	}
	app, err := s.store.AppByID(ctx, g.AppID)
	if err != nil {
		return fmt.Errorf("load app: %w", err)
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return fmt.Errorf("load account: %w", err)
	}
	restoreStore, ok := s.store.(state.ConfigRestoreStore)
	if !ok {
		return errors.New("atomic config restoration is unavailable")
	}
	revision, err := restoreStore.ApplyConfigRestore(ctx, state.ConfigRestore{
		Finish: state.ConfigChangeFinish{AccountID: acct.ID, AppID: app.ID, Scope: g.Scope,
			Actor: configGuardActor, Source: state.ConfigSourceGuardRestore, RestoredRevision: g.BaseRevision, Now: now},
		ExpectedVariables: variables, ExpectedSecrets: secrets, Variables: configVariableRestores(writes),
		Secrets: retained, Guard: &g, Decision: decision,
	})
	if errors.Is(err, state.ErrConfigRestoreChanged) {
		// Re-evaluate the fresh state next round.
		return nil
	}
	if err != nil {
		return fmt.Errorf("restore guarded config: %w", err)
	}
	decision.RestoredRevision = revision.Revision
	s.requestConfigRestart(ctx, app)
	s.emitConfigGuardOutcome(ctx, g, state.ConfigGuardRestored, decision)
	return nil
}

// requestConfigRestart replaces instances still running the failed config,
// through the runtime-config restart that `POST /restart?fresh=true` uses.
// A restart already in progress is left to finish; the next wakes already
// boot with the restored config.
func (s *server) requestConfigRestart(ctx context.Context, app state.App) {
	if s.notif == nil {
		return
	}
	claimed, err := claimAppRestart(ctx, s.store, app.ID)
	if err != nil || !claimed {
		s.log.Warn("config guard: restart not requested", "app", app.ID, "claimed", claimed, "err", err)
		return
	}
	wakeID := uuid.NewString()
	payload := fmt.Sprintf(`{"app_id":"%s","wake_id":"%s"}`, app.ID, wakeID)
	if err := s.notif.Notify(ctx, db.NotifyRuntimeConfigRestart, payload); err != nil {
		if releaseErr := releaseAppRestartClaim(ctx, s.store, app.ID); releaseErr != nil {
			s.log.Error("config guard: release failed restart claim", "app", app.ID, "err", releaseErr)
		}
		s.log.Warn("config guard: queue restart", "app", app.ID, "err", err)
	}
}

func (s *server) runConfigGuardWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(api.ConfigGuardCheckIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.configGuardSweep(bounded, time.Now().UTC()); err != nil && ctx.Err() == nil {
			s.log.Warn("config guard sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
