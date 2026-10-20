// adr: 20261019180000
package state

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrScenarioManagedBindings refuses contained enrollment of an app that holds
// real managed PostgreSQL or object-storage bindings: a contained member can
// never reach them, so the run would only fail later and less clearly.
var ErrScenarioManagedBindings = errors.New("state: contained rehearsal member has managed PostgreSQL or object storage bindings")

// appHasManagedBindingsLocked mirrors ScenarioActionAppHasManagedBindings.
// Callers hold m.mu.
func (m *MemStore) appHasManagedBindingsLocked(appID string) bool {
	for key, secret := range m.secrets {
		if key.AppID == appID && (secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "") {
			return true
		}
	}
	return false
}

// rehearsalBrokerTriggerLocked reports whether t polls a real external broker
// for a rehearsal-enrolled app; such a trigger would consume or commit offsets
// that production consumers own. Callers hold m.mu.
func (m *MemStore) rehearsalBrokerTriggerLocked(t sqlc.Trigger) bool {
	switch api.TriggerKind(t.Kind) {
	case api.TriggerKindKafka, api.TriggerKindNATS, api.TriggerKindRedisStreams, api.TriggerKindSQSCompat:
		app, ok := m.triggerAppLocked(t)
		if !ok {
			return false
		}
		_, enrolled := m.scenarioActionPolicies[app.ID]
		return enrolled
	}
	return false
}

// rehearsalAlertExcludedLocked reports whether an account-wide alert
// aggregate (scopedApp == "") must skip appID because the app is enrolled in
// a rehearsal: its candidate revision and injected faults must neither page
// the account nor mask an outage. App-scoped rules keep counting their app.
// Callers hold m.mu.
func (m *MemStore) rehearsalAlertExcludedLocked(scopedApp, appID string) bool {
	if scopedApp != "" || appID == "" {
		return false
	}
	_, enrolled := m.scenarioActionPolicies[appID]
	return enrolled
}
