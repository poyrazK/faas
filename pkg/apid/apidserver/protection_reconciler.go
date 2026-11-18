package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/releasegate"
	"github.com/onebox-faas/faas/pkg/state"
)

// Capabilities whose objects the baseline creates; an item whose capability
// is not released to the account is skipped as not_released.
const (
	protectionCapabilitySLOs     = "custom-slos"
	protectionCapabilityChannels = "notification-channels"
	protectionCapabilityAlerts   = "alerts"
	protectionCapabilityGates    = "release-gates"
)

// protectionCapabilityChecks releases synthetic checks.
const protectionCapabilityChecks = "synthetic-checks"

// protectionReconciledItems are the items this reconciler creates, in
// dependency order.
var protectionReconciledItems = []state.AppProtectionItem{
	state.AppProtectionAvailabilitySLO,
	state.AppProtectionLatencySLO,
	state.AppProtectionHealthCheck,
	state.AppProtectionEmailChannel,
	state.AppProtectionAvailabilityBurnAlert,
	state.AppProtectionLatencyBurnAlert,
	state.AppProtectionHealthAlert,
	state.AppProtectionAvailabilityGate,
}

// protectionOutcome is one item's result: created (ObjectID set), skipped
// (Reason set), or neither when a transient error leaves it for a retry.
type protectionOutcome struct {
	objectID string
	written  map[string]any
	reason   string
}

func protectionCreated(id string, written map[string]any) protectionOutcome {
	return protectionOutcome{objectID: id, written: written}
}

func protectionSkipped(reason string) protectionOutcome {
	return protectionOutcome{reason: reason}
}

// runProtectionReconciler creates the protection baseline for enrolled apps
// (ADR-20261116120000 §2, §4).
func (s *server) runProtectionReconciler(ctx context.Context) {
	ticker := time.NewTicker(api.ProtectionReconcileSweepInterval)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, time.Minute)
		if err := s.protectionReconcileSweep(bounded); err != nil && ctx.Err() == nil {
			s.log.Warn("protection reconcile sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// protectionReconcileSweep claims due enrolled apps and reconciles each.
func (s *server) protectionReconcileSweep(ctx context.Context) error {
	store, ok := s.store.(state.AppProtectionStore)
	if !ok {
		return nil
	}
	due, err := store.ClaimAppProtectionReconciles(ctx, api.ProtectionReconcileBatch, api.ProtectionReconcileLease)
	if err != nil {
		return fmt.Errorf("claim: %w", err)
	}
	for _, p := range due {
		next := s.reconcileAppProtection(ctx, store, p)
		if err := store.FinishAppProtectionReconcile(ctx, p.AppID, next); err != nil && !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("finish %s: %w", p.AppID, err)
		}
	}
	return nil
}

// reconcileAppProtection creates whatever baseline items an eligible app
// lacks and returns when to look at it again.
func (s *server) reconcileAppProtection(ctx context.Context, store state.AppProtectionStore, p state.AppProtection) time.Time {
	now := time.Now().UTC()
	retry, recheck := now.Add(api.ProtectionReconcileRetry), now.Add(api.ProtectionReconcileRecheck)
	app, err := s.store.AppByID(ctx, p.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return recheck
	}
	if err != nil {
		s.log.Warn("protection: load app", "app", p.AppID, "err", err)
		return retry
	}
	if !protectionEligibleApp(app) {
		return recheck
	}
	if live, err := s.hasLiveProductionDeployment(ctx, app.ID); err != nil || !live {
		if err != nil {
			s.log.Warn("protection: live deployment", "app", app.ID, "err", err)
		}
		return retry
	}
	acct, err := s.store.AccountByID(ctx, p.AccountID)
	if err != nil {
		s.log.Warn("protection: load account", "app", app.ID, "err", err)
		return retry
	}
	records, err := store.ListAppProtectionObjects(ctx, app.ID)
	if err != nil {
		s.log.Warn("protection: list objects", "app", app.ID, "err", err)
		return retry
	}
	created := map[state.AppProtectionItem]string{}
	for _, rec := range records {
		if rec.Status == state.AppProtectionObjectCreated {
			created[rec.Item] = rec.ObjectID
		}
	}
	complete := true
	for _, item := range protectionReconciledItems {
		if _, done := created[item]; done {
			continue
		}
		out, err := s.protectionCreateItem(ctx, acct, app, item, created)
		if err != nil {
			s.log.Warn("protection: create item", "app", app.ID, "item", item, "err", err)
			complete = false
			continue
		}
		if out.objectID != "" {
			created[item] = out.objectID
		} else {
			complete = false
		}
		if err := s.recordProtectionOutcome(ctx, store, acct, app, item, out); err != nil {
			s.log.Warn("protection: record item", "app", app.ID, "item", item, "err", err)
			complete = false
		}
	}
	if complete {
		return recheck
	}
	return retry
}

// recordProtectionOutcome stores an item's outcome and audits a creation.
func (s *server) recordProtectionOutcome(ctx context.Context, store state.AppProtectionStore, acct state.Account, app state.App,
	item state.AppProtectionItem, out protectionOutcome) error {
	rec := state.AppProtectionObject{AppID: app.ID, Item: item}
	if out.objectID != "" {
		written, err := json.Marshal(out.written)
		if err != nil {
			return err
		}
		rec.ObjectID, rec.Written = out.objectID, written
	} else {
		rec.Status, rec.Reason = state.AppProtectionObjectSkipped, out.reason
	}
	if err := store.RecordAppProtectionObject(ctx, rec); err != nil {
		return err
	}
	if out.objectID != "" {
		s.audit.Emit(ctx, "protection.object_created", &acct.ID, map[string]any{
			"app_id": app.ID, "item": string(item), "object_id": out.objectID,
		})
	}
	return nil
}

// protectionEligibleApp is ADR-20261116120000 §1: a live, public,
// request-mode production app.
func protectionEligibleApp(app state.App) bool {
	if app.DeletedAt != nil || app.PreviewOfSlug != "" {
		return false
	}
	if api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
		return false
	}
	mode := app.Manifest.ExecutionMode
	return mode == "" || mode == api.ExecutionModeRequest
}

// hasLiveProductionDeployment reports a live deployment in the default
// (production) scope. A deployment goes live only after its health check.
func (s *server) hasLiveProductionDeployment(ctx context.Context, appID string) (bool, error) {
	deps, err := s.store.LiveDeployments(ctx, appID)
	if err != nil {
		return false, err
	}
	for _, d := range deps {
		if state.IsProductionScope(d.Scope) {
			return true, nil
		}
	}
	return false, nil
}

// protectionCapabilityOn decides a capability's platform flag for work on
// the app, as capgate does for the matching public route.
func (s *server) protectionCapabilityOn(capability string, acct state.Account, app state.App) bool {
	if s.capGate == nil {
		return true
	}
	return s.capGate.Enabled(capability, appFlagSubject(acct, app))
}

// protectionCreateItem makes one item through the same validation, plan
// gates and quotas as its public endpoint. created maps the items already
// made to their object ids.
func (s *server) protectionCreateItem(ctx context.Context, acct state.Account, app state.App, item state.AppProtectionItem,
	created map[state.AppProtectionItem]string) (protectionOutcome, error) {
	switch item {
	case state.AppProtectionAvailabilitySLO:
		return s.protectionCreateSLO(ctx, acct, app, api.CreateSLORequest{
			Name: api.ProtectionBaselineAvailabilitySLOName, SLI: state.SLIAvailability,
			ObjectivePct: api.ProtectionBaselineAvailabilityObjectivePct, WindowDays: api.ProtectionBaselineSLOWindowDays,
		})
	case state.AppProtectionLatencySLO:
		return s.protectionCreateSLO(ctx, acct, app, api.CreateSLORequest{
			Name: api.ProtectionBaselineLatencySLOName, SLI: state.SLILatency,
			LatencyThresholdMS: api.ProtectionBaselineLatencyThresholdMS,
			ObjectivePct:       api.ProtectionBaselineLatencyObjectivePct, WindowDays: api.ProtectionBaselineSLOWindowDays,
		})
	case state.AppProtectionHealthCheck:
		return s.protectionHealthCheck(ctx, acct, app)
	case state.AppProtectionHealthAlert:
		return s.protectionHealthAlert(ctx, acct, app, created[state.AppProtectionHealthCheck], created[state.AppProtectionEmailChannel])
	case state.AppProtectionEmailChannel:
		return s.protectionEmailChannel(ctx, acct, app)
	case state.AppProtectionAvailabilityBurnAlert:
		return s.protectionBurnAlert(ctx, acct, app, api.ProtectionBaselineAvailabilityBurnAlertName,
			created[state.AppProtectionAvailabilitySLO], created[state.AppProtectionEmailChannel])
	case state.AppProtectionLatencyBurnAlert:
		return s.protectionBurnAlert(ctx, acct, app, api.ProtectionBaselineLatencyBurnAlertName,
			created[state.AppProtectionLatencySLO], created[state.AppProtectionEmailChannel])
	case state.AppProtectionAvailabilityGate:
		return s.protectionAvailabilityGate(ctx, acct, app, created[state.AppProtectionAvailabilitySLO])
	}
	return protectionOutcome{}, fmt.Errorf("protection: item %s is not reconciled here", item)
}

// protectionCreateSLO mirrors createSLO.
func (s *server) protectionCreateSLO(ctx context.Context, acct state.Account, app state.App, req api.CreateSLORequest) (protectionOutcome, error) {
	if !acct.Plan.PerAppMetricsAllowed() {
		return protectionSkipped(api.AppProtectionSkipPlan), nil
	}
	if !s.protectionCapabilityOn(protectionCapabilitySLOs, acct, app) {
		return protectionSkipped(api.AppProtectionSkipNotReleased), nil
	}
	store, ok := s.store.(state.SLOStore)
	if !ok {
		return protectionOutcome{}, errors.New("SLO definitions are unavailable")
	}
	objective, prob := api.ValidateCreateSLO(req)
	if prob != nil {
		return protectionOutcome{}, fmt.Errorf("baseline SLO %s is invalid: %s", req.Name, prob.Detail)
	}
	row, err := store.CreateSLO(ctx, state.SLO{
		AccountID: acct.ID, AppID: app.ID, Name: req.Name, SLI: req.SLI,
		LatencyThresholdMS: req.LatencyThresholdMS, ObjectiveBP: objective, WindowDays: req.WindowDays,
	}, api.MaxSLOsPerApp)
	switch {
	case errors.Is(err, state.ErrSLOLimit):
		return protectionSkipped(api.AppProtectionSkipLimitReached), nil
	case errors.Is(err, state.ErrConflict):
		return protectionSkipped(api.AppProtectionSkipNameTaken), nil
	case err != nil:
		return protectionOutcome{}, err
	}
	s.audit.Emit(ctx, "slo.created", &acct.ID, map[string]any{
		"slo_id": row.ID, "app_id": app.ID, "name": row.Name, "sli": row.SLI,
		"latency_threshold_ms": row.LatencyThresholdMS, "objective_bp": row.ObjectiveBP, "window_days": row.WindowDays,
		"source": "protection",
	})
	return protectionCreated(row.ID, map[string]any{
		"name": row.Name, "sli": row.SLI, "latency_threshold_ms": row.LatencyThresholdMS,
		"objective_bp": row.ObjectiveBP, "window_days": row.WindowDays,
	}), nil
}

// protectionEmailChannel mirrors createNotificationChannel for the
// account's own verified address. An email channel the account already has
// for that address is reused rather than duplicated (channels are account
// scoped, so every protected app of the account shares one).
func (s *server) protectionEmailChannel(ctx context.Context, acct state.Account, app state.App) (protectionOutcome, error) {
	if limits, ok := api.LimitsFor(acct.Plan); !ok || limits.AlertRuleLimitPerApp == 0 {
		return protectionSkipped(api.AppProtectionSkipPlan), nil
	}
	if !s.protectionCapabilityOn(protectionCapabilityChannels, acct, app) {
		return protectionSkipped(api.AppProtectionSkipNotReleased), nil
	}
	if acct.Email == "" || !acct.EmailVerified() {
		return protectionSkipped(api.AppProtectionSkipEmailUnverified), nil
	}
	store, ok := s.store.(state.NotificationChannelStore)
	if !ok {
		return protectionOutcome{}, errors.New("notification channels are unavailable")
	}
	existing, err := store.ListNotificationChannels(ctx, acct.ID)
	if err != nil {
		return protectionOutcome{}, err
	}
	for _, c := range existing {
		if c.Kind == alertchannels.KindEmail && strings.EqualFold(c.Email, acct.Email) {
			return protectionCreated(c.ID, map[string]any{"name": c.Name, "kind": c.Kind, "reused": true}), nil
		}
	}
	req, prob := api.NormalizeNotificationChannelRequest(api.CreateNotificationChannelRequest{
		Name: api.ProtectionBaselineEmailChannelName, Kind: alertchannels.KindEmail, Email: acct.Email,
	})
	if prob != nil {
		return protectionOutcome{}, fmt.Errorf("baseline channel is invalid: %s", prob.Detail)
	}
	in, prob := sealChannel(acct, req)
	if prob != nil {
		return protectionOutcome{}, fmt.Errorf("baseline channel: %s", prob.Detail)
	}
	row, err := store.CreateNotificationChannel(ctx, in, api.MaxNotificationChannelsPerAccount)
	switch {
	case errors.Is(err, state.ErrNotificationChannelLimit):
		return protectionSkipped(api.AppProtectionSkipLimitReached), nil
	case errors.Is(err, state.ErrConflict):
		return protectionSkipped(api.AppProtectionSkipNameTaken), nil
	case err != nil:
		return protectionOutcome{}, err
	}
	s.audit.Emit(ctx, "notification_channel.created", &acct.ID, map[string]any{
		"channel_id": row.ID, "name": row.Name, "kind": row.Kind, "target": row.TargetHint, "source": "protection",
	})
	return protectionCreated(row.ID, map[string]any{"name": row.Name, "kind": row.Kind}), nil
}

// protectionHealthCheck mirrors createSyntheticCheck for the hourly
// baseline check on the app's health path. apid probes it through the
// route-probe challenge, unbilled (§3), so it needs the route prober.
func (s *server) protectionHealthCheck(ctx context.Context, acct state.Account, app state.App) (protectionOutcome, error) {
	if !acct.Plan.PerAppMetricsAllowed() {
		return protectionSkipped(api.AppProtectionSkipPlan), nil
	}
	if !s.protectionCapabilityOn(protectionCapabilityChecks, acct, app) {
		return protectionSkipped(api.AppProtectionSkipNotReleased), nil
	}
	if !s.protectionHealthProbesAvailable() {
		return protectionSkipped(api.AppProtectionSkipProbesUnavailable), nil
	}
	store, ok := s.store.(state.SyntheticCheckStore)
	if !ok {
		return protectionOutcome{}, errors.New("synthetic checks are unavailable")
	}
	path := app.Manifest.HealthPath
	if path == "" {
		path = "/"
	}
	req, prob := api.NormalizeCreateSyntheticCheck(api.CreateSyntheticCheckRequest{
		Name: api.ProtectionBaselineHealthCheckName, Method: "GET", Path: path,
		IntervalSeconds: api.ProtectionBaselineHealthCheckIntervalSeconds,
	})
	if prob != nil {
		return protectionOutcome{}, fmt.Errorf("baseline health check is invalid: %s", prob.Detail)
	}
	row, err := store.CreateSyntheticCheck(ctx, state.SyntheticCheck{
		AccountID: acct.ID, AppID: app.ID, Name: req.Name, Method: req.Method, Path: req.Path,
		ExpectedStatus: req.ExpectedStatus, TimeoutMS: req.TimeoutMS, IntervalSeconds: req.IntervalSeconds,
	}, api.MaxSyntheticChecksPerApp)
	switch {
	case errors.Is(err, state.ErrSyntheticCheckLimit):
		return protectionSkipped(api.AppProtectionSkipLimitReached), nil
	case errors.Is(err, state.ErrConflict):
		return protectionSkipped(api.AppProtectionSkipNameTaken), nil
	case err != nil:
		return protectionOutcome{}, err
	}
	s.audit.Emit(ctx, "synthetic_check.created", &acct.ID, map[string]any{
		"check_id": row.ID, "app_id": app.ID, "name": row.Name, "method": row.Method, "path": row.Path,
		"interval_seconds": row.IntervalSeconds, "source": "protection",
	})
	return protectionCreated(row.ID, map[string]any{
		"name": row.Name, "method": row.Method, "path": row.Path, "interval_seconds": row.IntervalSeconds,
		"timeout_ms": row.TimeoutMS, "expected_status": row.ExpectedStatus,
	}), nil
}

// protectionBurnAlert is the channel-only fast-burn rule on one baseline
// SLO.
func (s *server) protectionBurnAlert(ctx context.Context, acct state.Account, app state.App, name, sloID, channelID string) (protectionOutcome, error) {
	if sloID == "" {
		return protectionSkipped(api.AppProtectionSkipNeedsSLO), nil
	}
	if channelID == "" {
		return protectionSkipped(api.AppProtectionSkipNeedsChannel), nil
	}
	return s.protectionAlertRule(ctx, acct, app, api.CreateAlertRuleRequest{
		Name: name, Metric: api.AlertRuleMetricSLOBudgetBurn, Comparison: string(state.AlertGt),
		Threshold: api.ProtectionBaselineBurnRate, WindowSpec: api.ProtectionBaselineBurnWindow,
		SLOID: sloID, ChannelIDs: []string{channelID},
	}, api.AppProtectionSkipNeedsSLO)
}

// protectionHealthAlert fires after consecutive failed baseline health
// checks.
func (s *server) protectionHealthAlert(ctx context.Context, acct state.Account, app state.App, checkID, channelID string) (protectionOutcome, error) {
	if checkID == "" {
		return protectionSkipped(api.AppProtectionSkipNeedsCheck), nil
	}
	if channelID == "" {
		return protectionSkipped(api.AppProtectionSkipNeedsChannel), nil
	}
	return s.protectionAlertRule(ctx, acct, app, api.CreateAlertRuleRequest{
		Name: api.ProtectionBaselineHealthAlertName, Metric: api.AlertRuleMetricSyntheticConsecutiveFailures,
		Comparison: api.ProtectionBaselineHealthComparison, Threshold: api.ProtectionBaselineHealthFailures,
		WindowSpec: api.ProtectionBaselineHealthWindow, SyntheticCheckID: checkID, ChannelIDs: []string{channelID},
	}, api.AppProtectionSkipNeedsCheck)
}

// protectionAlertRule mirrors createAlertRule for a channel-only rule.
// targetGone is the skip reason when the SLO or check the rule watches no
// longer exists (the customer deleted it).
func (s *server) protectionAlertRule(ctx context.Context, acct state.Account, app state.App, req api.CreateAlertRuleRequest, targetGone string) (protectionOutcome, error) {
	name := req.Name
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.AlertRuleLimitPerApp == 0 {
		return protectionSkipped(api.AppProtectionSkipPlan), nil
	}
	if !s.protectionCapabilityOn(protectionCapabilityAlerts, acct, app) ||
		s.alertMetricCapabilityProblem(appFlagSubject(acct, app), req.Metric) != nil {
		return protectionSkipped(api.AppProtectionSkipNotReleased), nil //nolint:nilerr // an unreleased metric is a recorded skip, not a sweep error
	}
	if prob := validateAlertRuleBody(req); prob != nil {
		return protectionOutcome{}, fmt.Errorf("baseline alert %s is invalid: %s", name, prob.Detail)
	}
	if s.validateSLOAlert(ctx, app.ID, req) != nil || s.validateSyntheticCheckAlert(ctx, app.ID, req) != nil {
		return protectionSkipped(targetGone), nil //nolint:nilerr // a deleted SLO or check is a recorded skip, not a sweep error
	}
	channelIDs, prob := s.validateRuleChannels(ctx, acct.ID, req.ChannelIDs)
	if prob != nil {
		// The customer deleted the channel.
		return protectionSkipped(api.AppProtectionSkipNeedsChannel), nil //nolint:nilerr // a recorded skip, not a sweep error
	}
	row, err := s.store.CreateAlertRuleIfUnderQuota(ctx, state.AlertRule{
		AccountID:                       acct.ID,
		CreatedByAccountID:              acct.ID,
		AppID:                           app.ID,
		Name:                            req.Name,
		Enabled:                         true,
		Metric:                          state.AlertMetric(req.Metric),
		Comparison:                      state.AlertComparison(req.Comparison),
		Threshold:                       req.Threshold,
		WindowSpec:                      state.AlertWindowSpec(req.WindowSpec),
		SLOID:                           req.SLOID,
		SyntheticCheckID:                req.SyntheticCheckID,
		Action:                          alertRuleActionFrom(nil),
		WebhookSecretSealed:             []byte{},
		CooldownMinutes:                 alertRuleCooldownFrom(nil),
		PostDeployRollbackWindowSeconds: alertRollbackWindowFrom(nil),
	}, limits)
	var quota *state.AlertRuleQuotaError
	switch {
	case errors.As(err, &quota):
		return protectionSkipped(api.AppProtectionSkipLimitReached), nil
	case errors.Is(err, state.ErrConflict):
		return protectionSkipped(api.AppProtectionSkipNameTaken), nil
	case err != nil:
		return protectionOutcome{}, err
	}
	if prob := s.bindRuleChannels(ctx, row, channelIDs); prob != nil {
		// bindRuleChannels deletes a channel-only rule it could not bind.
		return protectionOutcome{}, fmt.Errorf("bind channels: %s", prob.Detail)
	}
	s.audit.Emit(ctx, "alert_rule.created", &acct.ID, map[string]any{
		"rule_id": row.ID, "app_id": row.AppID, "name": row.Name, "metric": row.Metric,
		"comparison": row.Comparison, "threshold": row.Threshold, "window_spec": row.WindowSpec,
		"slo_id": row.SLOID, "synthetic_check_id": row.SyntheticCheckID, "enabled": row.Enabled,
		"cooldown_minutes": row.CooldownMinutes, "source": "protection",
	})
	return protectionCreated(row.ID, map[string]any{
		"name": row.Name, "metric": string(row.Metric), "comparison": string(row.Comparison),
		"threshold": row.Threshold, "window_spec": string(row.WindowSpec), "slo_id": row.SLOID,
		"synthetic_check_id": row.SyntheticCheckID, "channel_ids": channelIDs,
	}), nil
}

// protectionAvailabilityGate mirrors createReleaseGate for an slo gate on
// the baseline availability SLO.
func (s *server) protectionAvailabilityGate(ctx context.Context, acct state.Account, app state.App, sloID string) (protectionOutcome, error) {
	if sloID == "" {
		return protectionSkipped(api.AppProtectionSkipNeedsSLO), nil
	}
	if !s.protectionCapabilityOn(protectionCapabilityGates, acct, app) {
		return protectionSkipped(api.AppProtectionSkipNotReleased), nil
	}
	gates, ok := s.store.(state.ReleaseGateStore)
	slos, ok2 := s.store.(state.SLOStore)
	if !ok || !ok2 {
		return protectionOutcome{}, errors.New("release gates are unavailable")
	}
	if _, err := slos.GetSLO(ctx, app.ID, sloID); errors.Is(err, state.ErrNotFound) {
		return protectionSkipped(api.AppProtectionSkipNeedsSLO), nil
	} else if err != nil {
		return protectionOutcome{}, err
	}
	spec, prob := releasegate.FromRequest(api.CreateReleaseGateRequest{
		Name: api.ProtectionBaselineAvailabilityGateName, Kind: releasegate.KindSLO, SLO: api.ProtectionBaselineAvailabilitySLOName,
	})
	if prob != nil {
		return protectionOutcome{}, fmt.Errorf("baseline gate is invalid: %s", prob.Detail)
	}
	created, err := gates.CreateReleaseGate(ctx, state.ReleaseGate{
		AccountID: acct.ID, AppID: app.ID, Name: spec.Name, Kind: spec.Kind, SLOID: sloID,
		MaxBurnRate: spec.MaxBurnRate, MinSamples: spec.MinSamples, CreatedBy: api.ProtectionBaselineGateActor,
	}, api.ReleaseGatesPerAppMax, time.Now().UTC())
	switch {
	case errors.Is(err, state.ErrReleaseGateLimit):
		return protectionSkipped(api.AppProtectionSkipLimitReached), nil
	case errors.Is(err, state.ErrConflict):
		return protectionSkipped(api.AppProtectionSkipNameTaken), nil
	case err != nil:
		return protectionOutcome{}, err
	}
	s.audit.Emit(ctx, "release_gate.created", &acct.ID, map[string]any{
		"app": app.ID, "gate": created.Name, "kind": created.Kind, "actor": created.CreatedBy, "source": "protection",
	})
	return protectionCreated(created.ID, map[string]any{
		"name": created.Name, "kind": created.Kind, "slo_id": created.SLOID,
		"max_burn_rate": created.MaxBurnRate, "min_samples": created.MinSamples,
	}), nil
}
