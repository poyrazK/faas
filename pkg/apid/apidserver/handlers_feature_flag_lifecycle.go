package apidserver

// Flag lifecycle report and reconciler (ADR 20261114150000 §4).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	featureFlagLifecycleInterval = time.Hour
	featureFlagLifecycleBatch    = 100
	featureFlagLifecycleTimeout  = 30 * time.Second
	featureFlagLifecycleActor    = "system:flags-lifecycle"
	featureFlagLifecycleKind     = "flags.lifecycle_attention"
)

type featureFlagLifecycleResponse struct {
	ConfigVersion int64                 `json:"config_version"`
	GeneratedAt   time.Time             `json:"generated_at"`
	Flags         []flags.FlagLifecycle `json:"flags"`
}

// featureFlagLifecycle builds the report for one environment. evidence is
// read only when the plan keeps request telemetry and the store supports it.
func (s *server) featureFlagLifecycle(ctx context.Context, acct state.Account, scope state.FeatureFlagScope, environmentSlug string, now time.Time) (featureFlagLifecycleResponse, error) {
	store, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		return featureFlagLifecycleResponse{}, errors.New("feature flags unavailable")
	}
	var history []flags.HistoryVersion
	before := int64(0)
	for len(history) < flags.LifecycleHistoryLimit {
		page, err := store.ListFeatureFlagVersions(ctx, scope, before)
		if err != nil {
			return featureFlagLifecycleResponse{}, err
		}
		for _, v := range page {
			if len(history) < flags.LifecycleHistoryLimit {
				history = append(history, flags.HistoryVersion{Version: v.Version, CreatedAt: v.CreatedAt, Config: v.Config})
			}
		}
		if len(page) == 0 || len(page) < api.FlagsMaxHistoryPage {
			break
		}
		before = page[len(page)-1].Version
	}
	resp := featureFlagLifecycleResponse{GeneratedAt: now.UTC(), Flags: []flags.FlagLifecycle{}}
	if len(history) == 0 {
		return resp, nil
	}
	resp.ConfigVersion = history[0].Version
	evaluated, err := s.featureFlagEvaluatedRequests(ctx, acct, scope, environmentSlug, history[0].Config, now)
	if err != nil {
		return featureFlagLifecycleResponse{}, err
	}
	resp.Flags = flags.Lifecycle(history, evaluated, now)
	return resp, nil
}

// featureFlagEvaluatedRequests counts requests with a decision per enabled
// flag over the lifecycle window (capped at telemetry retention), or returns
// nil when the plan or store has no request evidence.
func (s *server) featureFlagEvaluatedRequests(ctx context.Context, acct state.Account, scope state.FeatureFlagScope, environmentSlug string, cfg flags.Config, now time.Time) (map[string]int64, error) {
	limits := api.MustLimitsFor(acct.Plan)
	evidence, ok := s.store.(state.FeatureFlagEvidenceStore)
	if !ok || !limits.DebugTelemetryEnabled || limits.DebugTelemetryRetentionDays <= 0 {
		return nil, nil
	}
	window := flags.LifecycleEvidenceWindow
	if retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour; retention < window {
		window = retention
	}
	apps, err := s.store.AppsForProject(ctx, acct.ID, scope.ProjectID)
	if err != nil {
		return nil, err
	}
	params := sqlc.FeatureFlagRequestOutcomesParams{
		EnvironmentSlug: environmentSlug,
		AccountID:       stringToPgUUID(scope.AccountID),
		ReceivedFrom:    pgtype.Timestamptz{Time: now.Add(-window), Valid: true},
		ReceivedUntil:   pgtype.Timestamptz{Time: now, Valid: true},
	}
	for _, app := range apps {
		if app.PreviewOfSlug == "" {
			params.AppIds = append(params.AppIds, stringToPgUUID(app.ID))
		}
	}
	counts := map[string]int64{}
	for _, f := range cfg.Flags {
		if !f.Enabled || f.Killed || len(params.AppIds) == 0 {
			continue
		}
		params.FlagKey = f.Key
		rows, err := evidence.FeatureFlagRequestOutcomes(ctx, params)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			counts[f.Key] += row.RequestCount
		}
	}
	return counts, nil
}

func (s *server) getFeatureFlagLifecycle(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	resp, err := s.featureFlagLifecycle(r.Context(), acct, scope, r.PathValue("environment"), time.Now())
	if err != nil {
		writeFeatureFlagError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

type featureFlagLifecycleReconciler struct {
	server             *server
	log                *slog.Logger
	afterEnvironmentID string
}

// startFeatureFlagLifecycleReconciler checks one page of environments per
// interval and records one flags.lifecycle_attention audit event per
// environment, flag, reason and configuration version. Like auto-advance, the
// customer_flags platform flag is checked on every pass.
func startFeatureFlagLifecycleReconciler(ctx context.Context, s *server, log *slog.Logger) {
	if s == nil || s.store == nil {
		return
	}
	if _, ok := s.store.(state.FeatureFlagEnvironmentLister); !ok {
		return
	}
	if _, ok := s.store.(state.AuditEventOutboxStore); !ok {
		return
	}
	r := &featureFlagLifecycleReconciler{server: s, log: log}
	go func() {
		ticker := time.NewTicker(featureFlagLifecycleInterval)
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

func (r *featureFlagLifecycleReconciler) runPass(ctx context.Context, now time.Time) {
	s := r.server
	if !s.platformFlagOn(platformflags.CustomerFlags) {
		return
	}
	lister := s.store.(state.FeatureFlagEnvironmentLister)
	outbox := s.store.(state.AuditEventOutboxStore)
	envs, err := lister.ListFeatureFlagEnvironments(ctx, r.afterEnvironmentID, featureFlagLifecycleBatch)
	if err != nil {
		if r.log != nil && !errors.Is(err, context.Canceled) {
			r.log.WarnContext(ctx, "flags lifecycle: list environments failed", "err", err)
		}
		return
	}
	if len(envs) < featureFlagLifecycleBatch {
		r.afterEnvironmentID = ""
	} else {
		r.afterEnvironmentID = envs[len(envs)-1].Scope.EnvironmentID
	}
	for _, env := range envs {
		if ctx.Err() != nil {
			return
		}
		envCtx, cancel := context.WithTimeout(ctx, featureFlagLifecycleTimeout)
		r.checkEnvironment(envCtx, outbox, env, now)
		cancel()
	}
}

func (r *featureFlagLifecycleReconciler) checkEnvironment(ctx context.Context, outbox state.AuditEventOutboxStore, env state.FeatureFlagAutoRolloutCandidate, now time.Time) {
	s := r.server
	acct, err := s.store.AccountByID(ctx, env.Scope.AccountID)
	if err != nil || acct.Status != state.AccountActive || !s.platformFlagFor(platformflags.CustomerFlags, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)}) {
		return
	}
	report, err := s.featureFlagLifecycle(ctx, acct, env.Scope, env.EnvironmentSlug, now)
	if err != nil {
		if r.log != nil {
			r.log.WarnContext(ctx, "flags lifecycle: report failed", "environment", env.Scope.EnvironmentID, "err", err)
		}
		return
	}
	if err := r.notifyLifecycle(ctx, acct, env, report.Flags, now); err != nil && r.log != nil {
		r.log.WarnContext(ctx, "flags lifecycle: notification failed", "environment", env.Scope.EnvironmentID, "err", err)
	}
	for _, f := range report.Flags {
		for _, reason := range f.Attention {
			data, _ := json.Marshal(map[string]any{
				"project_id": env.Scope.ProjectID, "project": env.ProjectSlug,
				"environment_id": env.Scope.EnvironmentID, "environment": env.EnvironmentSlug,
				"flag": f.Key, "reason": reason, "config_version": report.ConfigVersion,
				"owner": f.Owner, "expires_at": f.ExpiresAt, "last_changed_at": f.LastChangedAt,
			})
			key := fmt.Sprintf("flags-lifecycle:%s:%s:%s:%d", env.Scope.EnvironmentID, f.Key, reason, report.ConfigVersion)
			accountID := env.Scope.AccountID
			if _, err := outbox.EnqueueAuditEvent(ctx, featureFlagLifecycleActor, featureFlagLifecycleKind, &accountID, data, key); err != nil && r.log != nil {
				r.log.WarnContext(ctx, "flags lifecycle: record attention failed", "environment", env.Scope.EnvironmentID, "flag", f.Key, "err", err)
			}
		}
	}
}
