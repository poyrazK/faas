// adr: 570
package main

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// importedRoutePolicyCacheMax bounds the content-addressed compile cache. It
// is a memory bound for compiled documents, not a customer quota.
const importedRoutePolicyCacheMax = 1024

// loadImportedSnapshotPolicy compiles the OpenAPI document pinned with the
// app's routing snapshot. Compiled policies are keyed by the document digest,
// separately from the TTL cache: an entry can never describe another document,
// so it needs no invalidation fence and never leaks into the TTL read path.
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
	if m != nil {
		m.mu.RLock()
		policy, cached := m.imported[revision]
		m.mu.RUnlock()
		if cached {
			return policy, nil
		}
	}
	policy, err := compileOpenAPIDocument(contract.Document)
	if err != nil {
		return declaredRoutePolicy{}, err
	}
	policy.documentRevision = revision
	if m != nil {
		m.mu.Lock()
		if m.imported == nil || len(m.imported) >= importedRoutePolicyCacheMax {
			m.imported = make(map[string]declaredRoutePolicy)
		}
		m.imported[revision] = policy
		m.mu.Unlock()
	}
	return policy, nil
}
