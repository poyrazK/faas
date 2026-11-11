// adr: 20261103222927
package state

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ListAppResourceInventory(ctx context.Context, q AppResourceInventoryRequest) (AppResourceInventoryPage, error) {
	if err := q.Validate(); err != nil {
		return AppResourceInventoryPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return AppResourceInventoryPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return AppResourceInventoryPage{}, err
	}
	app, ok := m.apps[q.AppID]
	if !ok || app.AccountID != q.AccountID || app.OrgID != q.OrgID || app.Status == AppDeleted {
		return AppResourceInventoryPage{}, ErrNotFound
	}
	items := []api.AppResourceInventoryItem{}
	switch q.Kind {
	case "domains":
		for _, d := range m.domains {
			if d.AppID == q.AppID && d.EnvironmentID == "" {
				verified := !d.VerifiedAt.IsZero()
				items = append(items, api.AppResourceInventoryItem{ID: strings.ToLower(d.Domain), AppID: q.AppID, Scope: q.Scope, Domain: strings.ToLower(d.Domain), Verified: &verified, CertStatus: string(d.CertStatus)})
			}
		}
	case "crons":
		for _, c := range m.crons {
			if c.AppID == q.AppID {
				enabled := c.Enabled
				items = append(items, api.AppResourceInventoryItem{ID: c.ID, AppID: q.AppID, Scope: q.Scope, Schedule: c.Schedule, Path: c.Path, Timezone: c.Timezone, Enabled: &enabled})
			}
		}
	case "buckets":
		for _, b := range m.objectBuckets {
			if b.AppID == q.AppID && b.AccountID == q.AccountID && b.Scope == q.Scope && b.State != "deleted" && m.cloneBucketAccessibleLocked(b) {
				items = append(items, api.AppResourceInventoryItem{ID: b.ID, AppID: q.AppID, Scope: q.Scope, Name: b.Name, Region: b.Region, State: b.State})
			}
		}
	case "storage_bindings":
		for _, c := range m.objectS3Credentials {
			b, ok := m.objectBuckets[c.BucketID]
			if !ok || b.AppID != q.AppID || b.AccountID != q.AccountID || b.Scope != q.Scope || b.State == "deleted" || !m.cloneBucketAccessibleLocked(b) || c.AccountID != q.AccountID || c.ManagedAppID != q.AppID || c.ManagedScope != q.Scope || c.Status != ObjectS3CredentialStatusActive || c.RotationParentID != "" {
				continue
			}
			pending := false
			for _, stage := range m.objectS3Credentials {
				if stage.RotationParentID == c.ID && stage.AccountID == c.AccountID && stage.Status == ObjectS3CredentialStatusActive {
					pending = true
					break
				}
			}
			items = append(items, api.AppResourceInventoryItem{ID: c.ID, AppID: q.AppID, Scope: q.Scope, BucketID: b.ID, Bucket: b.Name, Prefix: c.ManagedPrefix, RotationPending: &pending})
		}
	case "databases":
		// Managed PostgreSQL has its own MemoryStore. Native wiring delegates to
		// that registry instead of inventing an empty database catalog here.
		return AppResourceInventoryPage{}, ErrAppResourceInventoryUnavailable
	}
	return PageAppResourceInventory(items, q), nil
}
