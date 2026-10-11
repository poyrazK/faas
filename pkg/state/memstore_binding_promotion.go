package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

var _ BindingPromotionStore = (*MemStore)(nil)

func (m *MemStore) BindingPromotionBackend() any { return m }

func (m *MemStore) ReadBindingPromotionRevision(_ context.Context, accountID, appID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return "", ErrNotFound
	}
	return m.bindingPromotionRevisionLocked(appID)
}

// Capture every config fact read by inventory, plus env/secret changes that
// precede their runtime stamp. Only the digest is retained, never these values.
func (m *MemStore) bindingPromotionRevisionLocked(appID string) (string, error) {
	facts := map[string]any{"app": m.apps[appID], "stamp": m.runtimeConfigChangedAt[appID], "plan": m.accounts[m.apps[appID].AccountID].Plan}
	for key, policy := range m.bindingReleasePolicies {
		if key.appID == appID {
			facts["release-policy/"+key.scope] = policy
		}
	}
	if len(m.apps[appID].Manifest.ServiceBindings) > 0 {
		revision, err := m.serviceBindingRevisionLocked(m.apps[appID].AccountID)
		if err != nil {
			return "", err
		}
		facts["service-dependencies"] = revision
	}
	for key, row := range m.envs {
		if key.AppID == appID {
			facts["env/"+key.Scope+"/"+key.Key] = row
		}
	}
	for key, row := range m.secrets {
		if key.AppID == appID {
			facts["secret/"+key.Scope+"/"+key.Key] = row
		}
	}
	for id, row := range m.deployments {
		if row.AppID == appID {
			facts["deployment/"+id] = []any{row, row.SecretReloadSignalKnown, row.SecretReloadSignal, row.OverrideEnvSecrets, row.Sidecars}
		}
	}
	for key, row := range m.secretRuntimeReloadObservations {
		if key.AppID == appID {
			facts["reload/"+key.Scope+"/"+key.Key+"/"+key.InstanceID+"/"+key.WorkloadName] = row
		}
	}
	for key, process := range m.secretRuntimeProcesses {
		if process.AppID == appID {
			facts["process/"+key.InstanceID+"/"+key.WorkloadName] = process
		}
	}
	for key, signal := range m.sidecarSecretReloadSignals {
		deploymentID, _, _ := strings.Cut(key, "\x00")
		if m.deployments[deploymentID].AppID == appID {
			facts["sidecar-reload/"+key] = signal
		}
	}
	for id, row := range m.appTasks {
		if row.AppID == appID && row.BindingVerification != nil {
			facts["probe/"+id] = []any{row.DeploymentID, row.DeploymentScope, row.BindingVerification, row.Status, row.CreatedAt, row.FinishedAt, row.StdoutTail, row.OutputTruncated, row.ExitCode}
		}
	}
	for id, row := range m.instances {
		if row.AppID == appID && (row.Kind == "" || row.Kind == "wake") && row.Mode != string(InstanceModeMirror) && State(row.State).CountsForRAM() {
			facts["instance/"+id] = []any{row.DeploymentID, row.Kind, row.Mode, row.State, row.StartedAt}
		}
	}
	for id, row := range m.objectBuckets {
		if row.AppID == appID {
			facts["bucket/"+id] = []any{row.Name, row.State, row.AccountID}
		}
	}
	for id, row := range m.objectS3Credentials {
		parent := m.objectS3Credentials[row.RotationParentID]
		if row.ManagedAppID == appID || parent.ManagedAppID == appID {
			facts["s3/"+id] = []any{row.AccountID, row.BucketID, row.Permission, row.Status, row.ManagedAppID, row.ManagedScope, row.ManagedPrefix, row.RotationParentID, row.RotationWakeID, row.SecretSealed, row.KID, row.CreatedAt}
		}
	}
	for id, row := range m.queueBindings {
		if row.AppID == appID {
			facts["queue/"+id] = row
		}
	}
	for id, row := range m.triggers {
		if sameDeploymentID(uuidString(row.AppID), appID) {
			facts["trigger/"+id] = row
			facts["consumer/"+id] = m.triggerConsumerHealth[id]
		}
	}
	for id, row := range m.outboundAppBindings {
		if row.AppID == appID {
			facts["outbound/"+id] = row
			facts["offer/"+row.ID] = m.outboundIntegrationOffers[row.ID]
			facts["credential/"+row.ID] = m.outboundCredentials[row.ID]
			facts["credential-revision/"+row.ID] = m.outboundCredentialRevisions[row.ID]
			facts["outbound-probe/"+row.ID] = m.outboundProbePolicies[row.ID]
		}
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return "", fmt.Errorf("state: snapshot binding promotion: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (m *MemStore) PromoteDeploymentWithBindings(ctx context.Context, id string, fence BindingPromotionFence, serving string) (BindingPromotionResult, error) {
	ctx = WithBindingReleaseFences(ctx, []BindingPromotionFence{fence})
	guard := &bindingTrafficGuard{fence: fence}
	var expected []string
	if serving != "" {
		expected = []string{serving}
	}
	deployment, err := m.updateDeploymentTraffic(ctx, id, 100, expected, guard)
	return BindingPromotionResult{Deployment: deployment, FromPercent: guard.fromPercent, CheckedAt: guard.checkedAt}, err
}

func (m *MemStore) checkBindingTrafficGuardLocked(deployment Deployment, guard *bindingTrafficGuard) error {
	revision, err := m.bindingPromotionRevisionLocked(deployment.AppID)
	if err != nil {
		return err
	}
	return guard.check(deployment, m.apps[deployment.AppID].AccountID, revision, time.Now())
}
