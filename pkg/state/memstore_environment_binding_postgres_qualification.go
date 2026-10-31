package state

import (
	"context"
	"sort"
	"time"
)

var _ EnvironmentQualificationPostgresStore = (*MemStore)(nil)

// MemStore has no managed PostgreSQL catalog: each managed secret in the
// candidate scope is one settled binding delivery.
func (m *MemStore) candidatePostgresDeliveriesLocked(appID, scope string) []candidatePostgresDelivery {
	var deliveries []candidatePostgresDelivery
	for key, s := range m.secrets {
		if key.AppID != appID || s.Scope != scope || s.ManagedPostgresBindingID == "" {
			continue
		}
		deliveries = append(deliveries, candidatePostgresDelivery{BindingID: s.ManagedPostgresBindingID, EnvironmentKey: s.Key,
			Access: s.ManagedPostgresAccess, CredentialGeneration: s.ManagedCredentialGeneration, DeliveryVersion: s.DeliveryVersion, Settled: true})
	}
	sort.Slice(deliveries, func(i, j int) bool { return deliveries[i].BindingID < deliveries[j].BindingID })
	return deliveries
}

func (m *MemStore) PendingEnvironmentQualificationPostgresChecks(ctx context.Context, graphID string) ([]EnvironmentQualificationPostgresCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var checks []EnvironmentQualificationPostgresCheck
	now := time.Now()
	for _, memory := range m.environmentGitOps {
		for _, r := range memory.qualifications {
			if r.GraphID != graphID || r.BindingContext == nil || r.LeaseUntil == nil || !now.Before(*r.LeaseUntil) {
				continue
			}
			for _, d := range m.candidatePostgresDeliveriesLocked(r.AppID, r.FrozenInputs.Scope) {
				key := qualificationPostgresReceiptKey(r.ID, r.Attempt, d.BindingID, d.CredentialGeneration, d.DeliveryVersion)
				if _, recorded := memory.bindingPostgres[key]; recorded || !candidatePostgresAccessSupported(d.Access) || d.DeliveryVersion < 1 {
					continue
				}
				s := m.secrets[secretKey{AppID: r.AppID, Scope: r.FrozenInputs.Scope, Key: d.EnvironmentKey}]
				checks = append(checks, EnvironmentQualificationPostgresCheck{RequestID: r.ID, Attempt: r.Attempt,
					AccountID: memory.source.AccountID, AppID: r.AppID, Scope: r.FrozenInputs.Scope, BindingID: d.BindingID,
					EnvironmentKey: d.EnvironmentKey, Access: d.Access, CredentialGeneration: d.CredentialGeneration,
					DeliveryVersion: d.DeliveryVersion, CredentialRef: s.ManagedCredentialRef,
					Ciphertext: append([]byte(nil), s.Ciphertext...), Kid: s.Kid, ValueHash: s.ValueHash})
			}
		}
	}
	sort.Slice(checks, func(i, j int) bool {
		if checks[i].RequestID != checks[j].RequestID {
			return checks[i].RequestID < checks[j].RequestID
		}
		return checks[i].BindingID < checks[j].BindingID
	})
	return checks, nil
}

func (m *MemStore) RecordEnvironmentQualificationPostgresReceipt(ctx context.Context, c EnvironmentQualificationPostgresCheck) error {
	if !validPostgresQualificationCheck(c) {
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
	for _, d := range m.candidatePostgresDeliveriesLocked(request.AppID, request.FrozenInputs.Scope) {
		current = current || d.BindingID == c.BindingID && d.EnvironmentKey == c.EnvironmentKey && d.Access == c.Access &&
			d.CredentialGeneration == c.CredentialGeneration && d.DeliveryVersion == c.DeliveryVersion
	}
	if !current {
		return ErrBindingPromotionChanged
	}
	if memory.bindingPostgres == nil {
		memory.bindingPostgres = map[string]time.Time{}
	}
	key := qualificationPostgresReceiptKey(c.RequestID, c.Attempt, c.BindingID, c.CredentialGeneration, c.DeliveryVersion)
	if _, exists := memory.bindingPostgres[key]; !exists {
		memory.bindingPostgres[key] = time.Now().UTC()
	}
	return nil
}
