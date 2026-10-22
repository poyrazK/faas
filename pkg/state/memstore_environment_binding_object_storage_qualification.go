package state

import (
	"context"
	"maps"
	"sort"
	"time"
)

var _ EnvironmentQualificationObjectStorageStore = (*MemStore)(nil)

// candidateObjectStorageDeliveriesLocked lists active managed S3 credentials
// (rotation stages excluded) with their scoped delivery versions.
func (m *MemStore) candidateObjectStorageDeliveriesLocked(appID, scope string) []candidateObjectStorageDelivery {
	var deliveries []candidateObjectStorageDelivery
	for _, c := range m.objectS3Credentials {
		if c.ManagedAppID != appID || c.ManagedScope != scope || c.Status != ObjectS3CredentialStatusActive || c.RotationParentID != "" {
			continue
		}
		d := candidateObjectStorageDelivery{CredentialID: c.ID, Prefix: c.ManagedPrefix, Permission: c.Permission, Deliveries: map[string]int64{}}
		for _, stage := range m.objectS3Credentials {
			d.Rotating = d.Rotating || stage.RotationParentID == c.ID && stage.Status == ObjectS3CredentialStatusActive
		}
		for key, s := range m.secrets {
			if key.AppID == appID && s.Scope == scope && s.ManagedObjectStorageCredentialID == c.ID {
				d.Deliveries[scope+"/"+s.Key] = s.DeliveryVersion
			}
		}
		deliveries = append(deliveries, d)
	}
	sort.Slice(deliveries, func(i, j int) bool { return deliveries[i].CredentialID < deliveries[j].CredentialID })
	return deliveries
}

func (m *MemStore) PendingEnvironmentQualificationObjectStorageChecks(ctx context.Context, graphID string) ([]EnvironmentQualificationObjectStorageCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var checks []EnvironmentQualificationObjectStorageCheck
	now := time.Now()
	for _, memory := range m.environmentGitOps {
		for _, r := range memory.qualifications {
			if r.GraphID != graphID || r.BindingContext == nil || r.LeaseUntil == nil || !now.Before(*r.LeaseUntil) {
				continue
			}
			scope := r.FrozenInputs.Scope
			for _, d := range m.candidateObjectStorageDeliveriesLocked(r.AppID, scope) {
				required := r.BindingContext.Policy.ObjectStorageRequirements[d.Prefix]
				if _, recorded := memory.bindingObjectStorage[qualificationObjectStorageReceiptKey(r.ID, r.Attempt, d.CredentialID)]; recorded || d.Rotating ||
					len(d.Deliveries) != 6 || !objectStorageModeCovers(d.Permission, required) {
					continue
				}
				check := EnvironmentQualificationObjectStorageCheck{RequestID: r.ID, Attempt: r.Attempt, AccountID: memory.source.AccountID,
					AppID: r.AppID, Scope: scope, CredentialID: d.CredentialID, Prefix: d.Prefix, Permission: d.Permission,
					Mode: candidateObjectStorageProbeMode(required, d.Permission)}
				for key, s := range m.secrets {
					if key.AppID == r.AppID && s.Scope == scope && s.ManagedObjectStorageCredentialID == d.CredentialID {
						check.Secrets = append(check.Secrets, EnvironmentQualificationObjectStorageSecret{Key: s.Key, DeliveryVersion: s.DeliveryVersion,
							Ciphertext: append([]byte(nil), s.Ciphertext...), Kid: s.Kid, ValueHash: s.ValueHash})
					}
				}
				sort.Slice(check.Secrets, func(i, j int) bool { return check.Secrets[i].Key < check.Secrets[j].Key })
				checks = append(checks, check)
			}
		}
	}
	sort.Slice(checks, func(i, j int) bool {
		if checks[i].RequestID != checks[j].RequestID {
			return checks[i].RequestID < checks[j].RequestID
		}
		return checks[i].CredentialID < checks[j].CredentialID
	})
	return checks, nil
}

func (m *MemStore) RecordEnvironmentQualificationObjectStorageReceipt(ctx context.Context, c EnvironmentQualificationObjectStorageCheck) error {
	if !validObjectStorageQualificationCheck(c) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, request, err := m.qualificationLocked(c.RequestID)
	if err != nil {
		return err
	}
	if request.Attempt != c.Attempt || request.BindingContext == nil || request.LeaseUntil == nil || !time.Now().Before(*request.LeaseUntil) {
		return ErrBindingPromotionChanged
	}
	current := false
	for _, d := range m.candidateObjectStorageDeliveriesLocked(request.AppID, request.FrozenInputs.Scope) {
		current = current || d.CredentialID == c.CredentialID && !d.Rotating && d.Prefix == c.Prefix && d.Permission == c.Permission &&
			maps.Equal(d.Deliveries, c.Deliveries())
	}
	if !current {
		return ErrBindingPromotionChanged
	}
	if memory.bindingObjectStorage == nil {
		memory.bindingObjectStorage = map[string]candidateObjectStorageReceipt{}
	}
	key := qualificationObjectStorageReceiptKey(c.RequestID, c.Attempt, c.CredentialID)
	if _, exists := memory.bindingObjectStorage[key]; !exists {
		memory.bindingObjectStorage[key] = candidateObjectStorageReceipt{Prefix: c.Prefix, Permission: c.Permission, Mode: c.Mode,
			Deliveries: c.Deliveries(), RecordedAt: time.Now().UTC()}
	}
	return nil
}
