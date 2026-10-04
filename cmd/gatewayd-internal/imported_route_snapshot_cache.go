// adr: 570
package main

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func (m *declaredRoutesMatcher) loadImportedSnapshotPolicy(app gateway.App) (declaredRoutePolicy, error) {
	contract := app.ImportedRoutePolicy
	if !contract.Found {
		policy := declaredRoutePolicy{missing: true}
		if err := sealDeclaredRoutePolicy(&policy); err != nil {
			return declaredRoutePolicy{}, err
		}
		return policy, nil
	}
	digest := sha256.Sum256(contract.Document)
	revision := hex.EncodeToString(digest[:])
	key := declaredRouteCacheKey{app.ID, app.AccountID}
	if m != nil {
		m.mu.RLock()
		policy, cached := m.entries[key]
		m.mu.RUnlock()
		if cached && policy.documentRevision == revision {
			return policy, nil
		}
	}
	policy, err := compileOpenAPIDocument(contract.Document)
	if err != nil {
		return declaredRoutePolicy{}, err
	}
	policy.documentRevision = revision
	if m != nil {
		policy.expires = m.now().Add(m.ttl)
		m.mu.Lock()
		m.entries[key] = policy
		m.mu.Unlock()
	}
	return policy, nil
}
