// adr: 638
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func maintenanceKey(scope string) string {
	return "gregale/durable-entities/v1/maintenance/" + scope + ".json"
}

func (m *Manager) claimMaintenance(ctx context.Context, scope, owner string) (maintenanceCheckpoint, string, bool, error) {
	value := maintenanceCheckpoint{Schema: 1, Scope: scope}
	body, etag, err := m.store.Get(ctx, maintenanceKey(scope), api.MaxDurableEntityMaintenanceBytes)
	if err == nil {
		if etag == "" || len(body) > api.MaxDurableEntityMaintenanceBytes || json.Unmarshal(body, &value) != nil || !validMaintenanceCheckpoint(scope, value) {
			return value, "", false, ErrCorrupt
		}
	} else if !errors.Is(err, ErrNotFound) {
		return value, "", false, err
	}
	if value.Owner != "" && m.now().Before(value.ExpiresAt) {
		return value, "", false, ErrBusy
	}
	if value.Epoch == ^uint64(0) {
		return value, "", false, ErrLimit
	}
	recovered := value.Owner != ""
	value.Epoch++
	value.Owner, value.Token = owner, uuid.NewString()
	value.ExpiresAt = m.now().UTC().Add(api.MaxDurableEntityLease)
	value.Revision = uuid.NewString()
	etag, err = m.putMaintenanceObject(ctx, maintenanceKey(scope), value, etag)
	return value, etag, recovered, err
}

func validMaintenanceCheckpoint(scope string, value maintenanceCheckpoint) bool {
	if value.Schema != 1 || value.Scope != scope || !validUUID(value.Revision) || value.Epoch == 0 ||
		len(value.Pending) > api.DurableEntityMaintenanceScanPageSize || len(value.Cursor) > api.MaxObjectS3ListCursorBytes || len(value.NextCursor) > api.MaxObjectS3ListCursorBytes {
		return false
	}
	if value.Owner == "" {
		return value.Token == "" && value.ExpiresAt.IsZero()
	}
	return validIdentity(value.Owner) && validUUID(value.Token) && !value.ExpiresAt.IsZero()
}

func (m *Manager) finishMaintenance(ctx context.Context, value maintenanceCheckpoint, etag string) error {
	if !m.now().Before(value.ExpiresAt) {
		return ErrStaleOwner
	}
	value.Owner, value.Token, value.ExpiresAt = "", "", time.Time{}
	value.Revision = uuid.NewString()
	_, err := m.putMaintenanceObject(ctx, maintenanceKey(value.Scope), value, etag)
	return err
}

func (m *Manager) putMaintenanceObject(ctx context.Context, key string, value any, etag string) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(body) > api.MaxDurableEntityMaintenanceBytes {
		return "", ErrLimit
	}
	version, err := m.store.Put(ctx, key, body, etag)
	if err != nil {
		return "", writeFailure("save maintenance progress", err)
	}
	if version == "" {
		return "", ErrUncertain
	}
	return version, nil
}
