package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) productDefinitionLocked(account, appID string, feature api.ProductFeature) (OperationDefinition, error) {
	definitions := m.operationMemoryLocked().definitions
	if feature.Operation == nil {
		def, ok := definitions[feature.DefinitionID]
		if !ok || def.AccountID != account || def.AppID != appID {
			return OperationDefinition{}, ErrProductBindingUnavailable
		}
		return cloneOperationDefinition(def), nil
	}
	var serving string
	for _, dep := range m.deployments {
		if dep.AppID != appID || dep.Scope != feature.Operation.Scope || dep.Status != DeployLive || dep.TrafficPercent <= 0 {
			continue
		}
		if serving != "" || dep.TrafficPercent != 100 {
			return OperationDefinition{}, ErrProductBindingUnavailable
		}
		serving = dep.ID
	}
	for _, def := range definitions {
		if serving != "" && def.AccountID == account && def.AppID == appID && def.DeploymentID == serving && def.Scope == feature.Operation.Scope && def.Spec.Name == feature.Operation.Name {
			return cloneOperationDefinition(def), nil
		}
	}
	return OperationDefinition{}, ErrProductBindingUnavailable
}

func (m *MemStore) ResolveProductFeature(_ context.Context, req ProductFeatureRequest) (OperationDefinition, error) {
	fingerprint, err := productRequestFingerprint(req)
	if err != nil {
		return OperationDefinition{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[req.AppID]
	if !ok || app.AccountID != req.AccountID || app.Status == AppDeleted {
		return OperationDefinition{}, ErrNotFound
	}
	if req.IdempotencyKey != "" {
		data := m.operationMemoryLocked()
		key := productOperationKey(req.AccountID, req.AppID, req.TenantID, req.Feature, req.IdempotencyKey)
		if receipt, ok := data.receipts[key]; ok {
			var original *Operation
			if op, ok := data.operations[receipt.OperationID]; ok && op.AccountID == req.AccountID && op.AppID == req.AppID && op.PlatformTenantID == req.TenantID {
				original = &op
			}
			id, err := productRetryDefinition(original, receipt.ExpiresAt, receipt.Fingerprint, fingerprint, time.Now())
			if err != nil {
				return OperationDefinition{}, err
			}
			if id != "" {
				return cloneOperationDefinition(data.definitions[id]), nil
			}
		}
	}
	d := m.productLocked()
	customer, ok := d.customers[req.AppID+"/"+req.TenantID]
	if !ok {
		return OperationDefinition{}, ErrProductAccess
	}
	catalog := d.revisions[req.AppID+"/"+customer.Revision]
	feature, _, err := productFeatureGrant(catalog, customer, req.Feature)
	if err != nil {
		return OperationDefinition{}, err
	}
	return m.productDefinitionLocked(req.AccountID, req.AppID, feature)
}

func (m *MemStore) GetProductReceipt(_ context.Context, account, appID, operation string) (ProductReceiptSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	row, exists := m.productLocked().receipts[operation]
	if !ok || app.AccountID != account || app.Status == AppDeleted || !exists || row.account != account || row.app != appID {
		return ProductReceiptSnapshot{}, ErrNotFound
	}
	// Return private evidence, including nested allowance and timestamp pointers.
	encoded, err := json.Marshal(productMemoryEvidence(operation, row))
	if err != nil {
		return ProductReceiptSnapshot{}, err
	}
	var result api.ProductReceiptEvidence
	err = json.Unmarshal(encoded, &result)
	out := ProductReceiptSnapshot{ProductReceiptEvidence: result}
	if op, ok := m.operationMemoryLocked().operations[operation]; ok && op.AccountID == account && op.AppID == appID && op.PlatformTenantID == row.tenant && operationRetained(op, time.Now()) {
		copy := cloneOperation(m.operationDeliveryLocked(op))
		out.Operation = &copy
	}
	return out, err
}
