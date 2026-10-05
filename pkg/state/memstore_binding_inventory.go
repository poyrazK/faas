package state

import (
	"context"
	"encoding/json"
	"sort"
)

var (
	_ ObjectStorageBindingInventoryStore = (*MemStore)(nil)
	_ QueueBindingConsumerInventoryStore = (*MemStore)(nil)
)

func (m *MemStore) ListObjectStorageBindingsForApp(_ context.Context, accountID, appID, scope string) ([]ObjectStorageBindingInventory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]ObjectStorageBindingInventory, 0)
	for _, c := range m.objectS3Credentials {
		b, ok := m.objectBuckets[c.BucketID]
		if !ok || b.AccountID != accountID || b.AppID != appID || b.State == "deleted" ||
			c.AccountID != accountID || c.ManagedAppID != appID || c.Status != ObjectS3CredentialStatusActive ||
			c.RotationParentID != "" || scope != "" && c.ManagedScope != scope {
			continue
		}
		pending := false
		wakeID := ""
		var latestRotation ObjectS3Credential
		for _, stage := range m.objectS3Credentials {
			if stage.RotationParentID != c.ID || stage.AccountID != accountID {
				continue
			}
			if latestRotation.ID == "" || stage.CreatedAt.After(latestRotation.CreatedAt) || stage.CreatedAt.Equal(latestRotation.CreatedAt) && stage.ID > latestRotation.ID {
				latestRotation = stage
			}
			if stage.Status == ObjectS3CredentialStatusActive {
				pending = true
				wakeID = stage.RotationWakeID
			}
		}
		items = append(items, ObjectStorageBindingInventory{
			RotationRevisionID: latestRotation.ID,
			BindingID:          c.ID,
			BucketName:         b.Name, Scope: c.ManagedScope, Prefix: c.ManagedPrefix,
			Permission: c.Permission, State: c.Status, RotationPending: pending, RotationWakeID: wakeID,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].BucketName+items[i].Prefix+items[i].Scope < items[j].BucketName+items[j].Prefix+items[j].Scope
	})
	return items, nil
}

func (m *MemStore) ListQueueBindingConsumersForApp(_ context.Context, accountID, appID string) ([]QueueBindingConsumerInventory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]QueueBindingConsumerInventory, 0)
	for _, b := range m.queueBindings {
		if b.AccountID != accountID || b.AppID != appID {
			continue
		}
		item := QueueBindingConsumerInventory{BindingID: b.ID}
		var selectedID string
		for id, trigger := range m.triggers {
			var marker struct {
				BindingID string `json:"queue_binding_id"`
			}
			if uuidString(trigger.AppID) != canonicalMemUUID(appID) || trigger.Kind != "queue" || trigger.Source.String != "queue" ||
				json.Unmarshal(trigger.Config, &marker) != nil || marker.BindingID != b.ID {
				continue
			}
			if selectedID != "" {
				selected := m.triggers[selectedID]
				if trigger.CreatedAt.Time.After(selected.CreatedAt.Time) || trigger.CreatedAt.Time.Equal(selected.CreatedAt.Time) && id > selectedID {
					continue
				}
			}
			selectedID = id
		}
		if selectedID != "" {
			enabled := m.triggers[selectedID].Enabled
			health := m.triggerConsumerHealth[selectedID]
			item.ConsumerEnabled = &enabled
			item.LastPollAt = cloneHealthTime(health.LastPollAt)
			item.LastSuccessAt = cloneHealthTime(health.LastSuccessAt)
			item.LastErrorAt = cloneHealthTime(health.LastErrorAt)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].BindingID < items[j].BindingID })
	return items, nil
}
