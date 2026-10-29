package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Departed-member integrations (ADR-20261026151103). When a member leaves an
// organization, the integrations they configured on its company apps stop
// sending company data to destinations they chose:
//
//   - webhook alert rules, log drains and app webhooks are paused (enabled =
//     false), for an admin to re-point and re-enable;
//   - alert rules that also act (rollback, demote, promote) keep acting but
//     lose the member's webhook;
//   - the member's own notification channels are unbound from the company
//     apps' rules.
//
// A row's author is its created_by_account_id; a row older than the column
// was created by the app's creator, the only member who could. A drain an
// application standard manages is the organization's own and is left alone.

// DepartedIntegrations counts what one departure changed.
type DepartedIntegrations struct {
	PausedAlertRules     int64
	ClearedAlertWebhooks int64
	UnboundAlertChannels int64
	PausedLogDrains      int64
	PausedWebhooks       int64
}

// activityData is the departure's part of the member-removal activity, so
// the organization's owners and admins see what was paused and why.
func (d DepartedIntegrations) activityData() map[string]any {
	return map[string]any{
		"paused_alert_rules":     d.PausedAlertRules,
		"cleared_alert_webhooks": d.ClearedAlertWebhooks,
		"unbound_alert_channels": d.UnboundAlertChannels,
		"paused_log_drains":      d.PausedLogDrains,
		"paused_webhooks":        d.PausedWebhooks,
	}
}

const pauseDepartedMemberIntegrationsSQL = `
WITH org_apps AS (
    SELECT id, account_id FROM apps WHERE org_id = $1
), paused_alerts AS (
    UPDATE alert_rules r SET enabled = false, updated_at = now()
      FROM org_apps a
     WHERE r.app_id = a.id AND r.enabled AND r.action = 'webhook'
       AND coalesce(r.created_by_account_id, a.account_id) = $2
    RETURNING r.id
), cleared_alerts AS (
    UPDATE alert_rules r SET webhook_url = '', webhook_secret_sealed = ''::bytea, updated_at = now()
      FROM org_apps a
     WHERE r.app_id = a.id AND r.action <> 'webhook' AND r.webhook_url <> ''
       AND coalesce(r.created_by_account_id, a.account_id) = $2
    RETURNING r.id
), unbound_channels AS (
    DELETE FROM alert_rule_channels c
     USING alert_rules r, org_apps a, notification_channels n
     WHERE c.rule_id = r.id AND r.app_id = a.id AND n.id = c.channel_id AND n.account_id = $2
    RETURNING c.rule_id
), paused_drains AS (
    UPDATE app_log_drains d SET enabled = false, updated_at = now()
      FROM org_apps a
     WHERE d.app_id = a.id AND d.enabled
       AND coalesce(d.created_by_account_id, a.account_id) = $2
       AND application_standard_log_binding(d.app_id, d.id) IS NULL
    RETURNING d.id
), paused_webhooks AS (
    UPDATE app_webhooks h SET enabled = false, updated_at = now()
      FROM org_apps a
     WHERE h.app_id = a.id AND h.scope = 'app' AND h.enabled
       AND coalesce(h.created_by_account_id, a.account_id) = $2
    RETURNING h.id
)
SELECT (SELECT count(*) FROM paused_alerts), (SELECT count(*) FROM cleared_alerts),
       (SELECT count(*) FROM unbound_channels), (SELECT count(*) FROM paused_drains),
       (SELECT count(*) FROM paused_webhooks)`

// pauseDepartedMemberIntegrationsTx runs the departure in the transaction
// that removes the member.
func pauseDepartedMemberIntegrationsTx(ctx context.Context, tx pgx.Tx, orgID, accountID string) (DepartedIntegrations, error) {
	var d DepartedIntegrations
	err := tx.QueryRow(ctx, pauseDepartedMemberIntegrationsSQL, orgID, accountID).Scan(
		&d.PausedAlertRules, &d.ClearedAlertWebhooks, &d.UnboundAlertChannels, &d.PausedLogDrains, &d.PausedWebhooks)
	if err != nil {
		return DepartedIntegrations{}, fmt.Errorf("state: pause departed member integrations: %w", err)
	}
	return d, nil
}

// pauseDepartedMemberIntegrationsLocked mirrors the PgStore departure.
func (m *MemStore) pauseDepartedMemberIntegrationsLocked(orgID, accountID string) DepartedIntegrations {
	now := time.Now().UTC()
	var d DepartedIntegrations
	orgApp := func(appID string) (App, bool) {
		app, ok := m.apps[appID]
		return app, ok && app.OrgID == orgID
	}
	authored := func(createdBy string, app App) bool {
		if createdBy != "" {
			return createdBy == accountID
		}
		return app.AccountID == accountID
	}
	for id, r := range m.alertRules {
		app, ok := orgApp(r.AppID)
		if !ok || !authored(r.CreatedByAccountID, app) {
			continue
		}
		webhookOnly := r.Action == AlertActionWebhook || r.Action == ""
		switch {
		case webhookOnly && r.Enabled:
			r.Enabled = false
			d.PausedAlertRules++
		case !webhookOnly && r.WebhookURL != "":
			r.WebhookURL, r.WebhookSecretSealed = "", []byte{}
			d.ClearedAlertWebhooks++
		default:
			continue
		}
		r.UpdatedAt = now
		m.alertRules[id] = r
	}
	owned := map[string]bool{}
	for _, c := range m.notificationChannels[accountID] {
		owned[c.ID] = true
	}
	for ruleID, channels := range m.alertRuleChannels {
		if _, ok := orgApp(m.alertRules[ruleID].AppID); !ok {
			continue
		}
		kept := channels[:0:0]
		for _, c := range channels {
			if owned[c] {
				d.UnboundAlertChannels++
				continue
			}
			kept = append(kept, c)
		}
		m.alertRuleChannels[ruleID] = kept
	}
	for id, drain := range m.appLogDrains {
		app, ok := orgApp(drain.AppID)
		if !ok || !drain.Enabled || !authored(drain.CreatedByAccountID, app) || m.standardLogBindingLocked(drain) != nil {
			continue
		}
		drain.Enabled, drain.UpdatedAt = false, now
		m.appLogDrains[id] = drain
		d.PausedLogDrains++
	}
	for id, hook := range m.appWebhooks {
		app, ok := orgApp(hook.AppID)
		if !ok || hook.Scope != AppWebhookScopeApp && hook.Scope != "" || !hook.Enabled || !authored(hook.CreatedByAccountID, app) {
			continue
		}
		hook.Enabled, hook.UpdatedAt = false, now
		m.appWebhooks[id] = hook
		d.PausedWebhooks++
	}
	return d
}

// accountMaySeeOrgLocked mirrors account_may_see_org_resources: an app or
// project of no organization or of a personal one is its account's, and a
// company's only while the account is an active member of it.
func (m *MemStore) accountMaySeeOrgLocked(accountID, orgID string) bool {
	if orgID == "" {
		return true
	}
	org, ok := m.orgs[orgID]
	if !ok {
		return false
	}
	if org.Personal {
		return true
	}
	if org.DeletedPending || org.Status == OrgStatusDeletedPending {
		return false
	}
	member, ok := m.memberships[orgAccountKey{OrgID: orgID, AccountID: accountID}]
	if !ok || member.RemovedAt != nil {
		return false
	}
	switch member.Role {
	case OrgRoleOwner, OrgRoleAdmin, OrgRoleDeveloper, OrgRoleViewer:
		return true
	}
	return false
}
