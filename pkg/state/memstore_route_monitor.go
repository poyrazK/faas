package state

import (
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Match the SQL policy triggers; timestamps and unrelated app edits are no-ops.
func routeCheckAppInputsChanged(before, after App) bool {
	return before.Slug != after.Slug || before.Type != after.Type || before.ConsumerAuthMode != after.ConsumerAuthMode ||
		before.MaintenanceMode != after.MaintenanceMode || before.Manifest.RequestTimeoutS != after.Manifest.RequestTimeoutS ||
		before.RAMMB != after.RAMMB || before.CPUMillicores != after.CPUMillicores || before.MaxConcurrency != after.MaxConcurrency ||
		!reflect.DeepEqual(before.RequestRateLimitRPS, after.RequestRateLimitRPS) || !reflect.DeepEqual(before.RequestRateLimitBurst, after.RequestRateLimitBurst) || !reflect.DeepEqual(before.ScalingPolicy, after.ScalingPolicy)
}

func routeCheckRuleInputsChanged(before, after EdgeRule) bool {
	before.CreatedAt, before.UpdatedAt, after.CreatedAt, after.UpdatedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	before.ManifestKey, after.ManifestKey = "", ""
	before.MatchHeaders, after.MatchHeaders = cloneEdgeRuleMatchHeaders(before.MatchHeaders), cloneEdgeRuleMatchHeaders(after.MatchHeaders)
	return !reflect.DeepEqual(before, after)
}

func (m *MemStore) enqueueRoutePolicyChecksLocked(appID string) {
	var targets []string
	for id, deployment := range m.deployments {
		if deployment.AppID != appID || deployment.Status != DeployLive {
			continue
		}
		_, checked := m.automaticRouteChecks[id]
		capture, captured := m.openAPIDocs[id]
		app := m.apps[appID]
		if checked || captured && capture.AppID == appID && capture.AccountID == app.AccountID {
			targets = append(targets, id)
		}
	}
	sort.Strings(targets)
	for _, id := range targets {
		m.enqueueAutomaticRouteCheckLocked(appID, id, true)
	}
}

func (m *MemStore) enqueueRouteAccountChangeLocked(before, after Account) {
	if before.Plan == after.Plan && before.Status == after.Status && before.AbuseHeld() == after.AbuseHeld() {
		return
	}
	for id, app := range m.apps {
		if app.AccountID == after.ID {
			m.enqueueRoutePolicyChecksLocked(id)
		}
	}
}

// Call while holding m.mu, before publishing the completed record. Unknown
// checks retain the last confirmed state, including a still-open violation.
func (m *MemStore) routeSafetyTransitionLocked(item *memAutomaticRouteCheck, check api.RouteRequirementsCheck, now time.Time) error {
	app, ok := m.apps[item.Claim.AppID]
	account := m.accounts[item.Claim.AccountID]
	deployment := m.deployments[item.Claim.DeploymentID]
	if !ok || app.AccountID != account.ID || app.Status == AppDeleted || deployment.AppID != app.ID || deployment.Status != DeployLive || !account.MayDeploy() || account.Plan.OpenAPIDocsPerDeployment() <= 0 {
		return nil
	}
	status, previous := check.Report.Status, item.SafetyState
	if previous == "" {
		previous = "unknown"
	}
	if status != "satisfied" && status != "violated" || status == previous {
		return nil
	}
	item.SafetyState = status
	event := AppWebhookEventRouteRequirementsViolated
	if status == "satisfied" {
		if previous != "violated" {
			return nil
		}
		event = AppWebhookEventRouteRequirementsRecovered
	}
	payload := map[string]any{
		"app_id": app.ID, "deployment_id": deployment.ID, "status": status, "previous_status": previous,
		"transition_id": item.Claim.RequestID, "checked_at": now,
		"requirements_revision": check.RequirementsRevision, "requirements_sha256": check.RequirementsSHA256,
		"configuration_sha256": check.ConfigurationSHA256, "capture_sha256": item.Record.CaptureSHA256,
		"result_path": "/v1/apps/" + app.Slug + "/route-requirements/checks/" + deployment.ID,
	}
	return m.enqueueRouteCheckNotificationLocked(item, event, payload, now)
}

func (m *MemStore) enqueueRouteCheckNotificationLocked(item *memAutomaticRouteCheck, event AppWebhookEvent, payload map[string]any, now time.Time) error {
	var recipients []string
	for id, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == item.Claim.AppID && hook.AccountID == item.Claim.AccountID && hook.Enabled && appWebhookMatches(hook.EventFilter, event) {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	sort.Strings(recipients)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	id := uuid.NewString()
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: item.Claim.AccountID, AppID: item.Claim.AppID,
		Event: event, SourceID: item.Claim.RequestID, Payload: body, RecipientWebhookIDs: recipients, CreatedAt: now}
	return nil
}

func (m *MemStore) storeRouteCheckAccountLocked(id string, after Account) {
	before := m.accounts[id]
	m.accounts[id] = after
	m.enqueueRouteAccountChangeLocked(before, after)
}

func (m *MemStore) routeFindingTransitionLocked(item *memAutomaticRouteCheck, previous string, check api.RouteRequirementsCheck, now time.Time) error {
	changes := item.Record.Changes
	if previous != "violated" || changes == nil || changes.Status != "comparable" || changes.Summary.NewlyViolated == 0 {
		return nil
	}
	app := m.apps[item.Claim.AppID]
	account := m.accounts[item.Claim.AccountID]
	deployment := m.deployments[item.Claim.DeploymentID]
	if app.AccountID != account.ID || app.Status == AppDeleted || deployment.AppID != app.ID || deployment.Status != DeployLive || !account.MayDeploy() || account.Plan.OpenAPIDocsPerDeployment() <= 0 {
		return nil
	}
	path := "/v1/apps/" + app.Slug + "/route-requirements/checks/" + deployment.ID
	return m.enqueueRouteCheckNotificationLocked(item, AppWebhookEventRouteRequirementsChanged, map[string]any{
		"app_id": app.ID, "deployment_id": deployment.ID, "status": check.Report.Status,
		"transition_id": item.Claim.RequestID, "checked_at": now,
		"requirements_revision": check.RequirementsRevision, "requirements_sha256": check.RequirementsSHA256,
		"configuration_sha256": check.ConfigurationSHA256, "capture_sha256": item.Record.CaptureSHA256,
		"summary": changes.Summary, "comparison_status": changes.Status, "result_path": path,
		"history_path": path + "/history/" + item.Claim.RequestID,
	}, now)
}
