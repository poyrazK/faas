package state

import (
	"encoding/json"
	"net/url"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) prepareProfileAlertsLocked(o profileAlertObservation) ([]profileAlertPlan, error) {
	if o.Assessment.Baseline.DeploymentID == "" || o.Assessment.Candidate.DeploymentID == "" {
		return nil, nil
	}
	p := m.profileDeploymentPolicies[o.AppID]
	app := m.apps[o.AppID]
	if app.Status == AppDeleted || p.Revision != o.Revision || !p.Config.Enabled || (!p.Config.NotifyRouteRegressions && o.Source != "periodic") {
		return nil, nil
	}
	o.Slug, o.Config = app.Slug, p.Config
	allowed := map[string]bool{}
	for _, route := range p.Config.Options.Routes {
		allowed[route] = true
	}
	var plans []profileAlertPlan
	for _, r := range o.Assessment.RouteChecks {
		if !allowed[r.Route] {
			continue
		}
		key := profileAlertKey(o, r.Route)
		plan, err := profileAlertTransition(m.profileAlertStates[key], o, r)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func profileAlertHasEvent(plans []profileAlertPlan) bool {
	for _, p := range plans {
		if p.Event != "" {
			return true
		}
	}
	return false
}

// Enrichment changes only a host-owned string after all numerical payloads
// have been serialized. The saved investigation and outbox share the lock/tx.
func profileAlertInvestigation(plans []profileAlertPlan, slug, id string) {
	if id == "" {
		return
	}
	for i := range plans {
		if plans[i].Event == "" {
			continue
		}
		var payload api.ProfileRouteAlertPayload
		_ = json.Unmarshal(plans[i].Payload, &payload)
		payload.InvestigationPath = "/dashboard/apps/" + url.PathEscape(slug) + "/profiles?investigation_id=" + url.QueryEscape(id) + "#diff-flamegraph"
		plans[i].Payload, _ = json.Marshal(payload)
	}
}

func (m *MemStore) publishProfileAlertsLocked(o profileAlertObservation, plans []profileAlertPlan) {
	if m.profileAlertStates == nil {
		m.profileAlertStates = map[string]profileAlertState{}
	}
	for _, plan := range plans {
		m.profileAlertStates[plan.Key] = plan.State
		if plan.Event == "" || (o.Source == "periodic" && !o.Config.NotifyRouteRegressions) {
			continue
		}
		var recipients []string
		for id, h := range m.appWebhooks {
			if h.Scope == AppWebhookScopeApp && h.AppID == o.AppID && h.AccountID == o.AccountID && h.Enabled && appWebhookMatches(h.EventFilter, plan.Event) {
				recipients = append(recipients, id)
			}
		}
		if len(recipients) == 0 {
			continue
		}
		sort.Strings(recipients)
		if m.appWebhookEventOutbox == nil {
			m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
		}
		id := uuid.NewString()
		m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: o.AccountID, AppID: o.AppID, Event: plan.Event, SourceID: plan.SourceID, Payload: plan.Payload, RecipientWebhookIDs: recipients, CreatedAt: o.Assessment.CheckedAt}
	}
}
