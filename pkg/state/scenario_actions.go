package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const ScenarioWebhookCaptureMode = "capture"
const ScenarioWebhookSuppressed = "rehearsal: unsupported webhook delivery suppressed"

type scenarioActionPolicy struct {
	Member             ScenarioTestMember
	SealedAt           time.Time
	Snapshot           []byte
	ManagedWorkClosing bool
	ManagedWorkFailed  bool
	RecorderClosing    bool
	Recorders          map[string]scenarioRecorderSession
	SimulationFailed   bool
	SimulationAttempts []api.ScenarioSimulationAttempt
}

// A policy is permanent for this app identity. Namespace pruning and soft
// deletion must never turn captured or suppressed work into live deliveries.
func (s *PgStore) ScenarioWebhookDeliveryBlocked(ctx context.Context, appID, webhookID string) (bool, error) {
	return sqlc.New().ScenarioWebhookDeliveryBlocked(ctx, s.pool, sqlc.ScenarioWebhookDeliveryBlockedParams{AppID: appID, WebhookID: webhookID})
}

func (m *MemStore) ScenarioWebhookDeliveryBlocked(ctx context.Context, appID, webhookID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scenarioWebhookBlockedLocked(appID, webhookID), nil
}

func (m *MemStore) scenarioWebhookBlockedLocked(appID, webhookID string) bool {
	_, byApp := m.scenarioActionPolicyLocked(appID)
	hook := m.appWebhooks[webhookID]
	if hook.ID == "" {
		for id, candidate := range m.appWebhooks {
			if canonicalMemUUID(id) == canonicalMemUUID(webhookID) {
				hook = candidate
				break
			}
		}
	}
	_, byReceiver := m.scenarioActionPolicyLocked(hook.AppID)
	return byApp || byReceiver
}

func (m *MemStore) scenarioActionPolicyLocked(appID string) (scenarioActionPolicy, bool) {
	if policy, exists := m.scenarioActionPolicies[appID]; exists {
		return policy, true
	}
	for id, policy := range m.scenarioActionPolicies {
		if canonicalMemUUID(id) == canonicalMemUUID(appID) {
			return policy, true
		}
	}
	return scenarioActionPolicy{}, false
}

func (m *MemStore) suppressScenarioWebhookLocked(row *AppWebhookDelivery) {
	if m.scenarioWebhookBlockedLocked(row.AppID, row.WebhookID) &&
		(row.Status == AppWebhookDeliveryPending || row.Status == AppWebhookDeliveryFailed || row.Status == AppWebhookDeliveryInFlight) {
		row.Status, row.LastError, row.UpdatedAt = AppWebhookDeliveryDead, ScenarioWebhookSuppressed, time.Now().UTC()
	}
}

func (m *MemStore) scenarioActionAppUnstartedLocked(appID string) bool {
	if _, exists := m.scenarioActionPolicies[appID]; exists {
		return false
	}
	for _, inst := range m.instances {
		if inst.AppID == appID {
			return false
		}
	}
	for _, d := range m.deployments {
		if d.AppID == appID {
			return false
		}
	}
	for _, o := range m.exclusiveOperations {
		if o.AppID == appID {
			return false
		}
	}
	for _, r := range m.workflowRuns {
		if r.AppID == appID {
			return false
		}
	}
	for _, d := range m.appWebhookDeliveries {
		if d.AppID == appID || m.appWebhooks[d.WebhookID].AppID == appID {
			return false
		}
	}
	return true
}

func (m *MemStore) scenarioCaptureRunLocked(accountID, appID string) (string, error) {
	policy, exists := m.scenarioActionPolicyLocked(appID)
	if !exists {
		return "", nil
	}
	registered := policy.Member
	member, active := m.scenarioTestMembers[registered.AppID]
	if !policy.SealedAt.IsZero() || !active || member.RunID != registered.RunID || canonicalMemUUID(registered.AccountID) != canonicalMemUUID(accountID) || !eligibleScenarioTestApp(m.apps[registered.AppID], registered.AccountID) {
		return "", ErrOperationEffectDestination
	}
	return registered.RunID, nil
}

func (m *MemStore) scenarioReceiverFingerprintLocked(hook AppWebhook) (string, error) {
	tenantReference := ""
	if hook.Scope == AppWebhookScopePlatformTenant {
		for id, tenant := range m.platformTenants {
			if canonicalMemUUID(id) == canonicalMemUUID(hook.PlatformTenantID) {
				tenantReference = tenant.ExternalRef
				break
			}
		}
		if tenantReference == "" {
			return "", ErrOperationEffectDestination
		}
	}
	return api.ScenarioReceiverFingerprint(string(hook.Scope), tenantReference, hook.TargetURL, string(hook.DeliveryFormat), string(hook.RetryPolicy)), nil
}

func scenarioReceiverFingerprintTx(ctx context.Context, tx sqlc.DBTX, webhookID string) (string, error) {
	hook, err := sqlc.New().ScenarioCaptureReceiver(ctx, tx, webhookID)
	if err != nil {
		return "", err
	}
	if hook.Scope == string(AppWebhookScopePlatformTenant) && hook.TenantReference == "" {
		return "", ErrOperationEffectDestination
	}
	return api.ScenarioReceiverFingerprint(hook.Scope, hook.TenantReference, hook.TargetUrl, hook.DeliveryFormat, hook.RetryPolicy), nil
}

func scenarioCaptureRunTx(ctx context.Context, tx sqlc.DBTX, accountID, appID string) (string, error) {
	policy, err := sqlc.New().ScenarioWebhookCapture(ctx, tx, sqlc.ScenarioWebhookCaptureParams{AccountID: accountID, AppID: appID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !policy.Active.Valid || !policy.Active.Bool {
		return "", ErrOperationEffectDestination
	}
	return policy.RunID, nil
}
