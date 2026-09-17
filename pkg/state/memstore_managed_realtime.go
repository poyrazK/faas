package state

import (
	"context"
	"sort"
	"time"
)

// CreateManagedRealtimeEndpointIfUnderQuota mirrors the Postgres transaction:
// the in-memory mutex covers the count and insert so concurrent tests cannot
// oversubscribe either cap.
func (m *MemStore) CreateManagedRealtimeEndpointIfUnderQuota(_ context.Context, in ManagedRealtimeEndpoint, perApp, perAccount int) (ManagedRealtimeEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted || app.AccountID != in.AccountID {
		return ManagedRealtimeEndpoint{}, ErrNotFound
	}
	for _, existing := range m.managedRealtimeEndpoints {
		if existing.ID == in.ID && in.ID != "" {
			return ManagedRealtimeEndpoint{}, ErrConflict
		}
	}
	appCount, accountCount := 0, 0
	for _, existing := range m.managedRealtimeEndpoints {
		if existing.AppID == in.AppID {
			appCount++
		}
		if existing.AccountID == in.AccountID {
			if owner, exists := m.apps[existing.AppID]; exists && owner.Status != AppDeleted {
				accountCount++
			}
		}
	}
	if appCount >= perApp {
		return ManagedRealtimeEndpoint{}, &ManagedRealtimeEndpointQuotaError{Scope: ManagedRealtimeEndpointQuotaScopeApp, Limit: perApp, Observed: appCount}
	}
	if accountCount >= perAccount {
		return ManagedRealtimeEndpoint{}, &ManagedRealtimeEndpointQuotaError{Scope: ManagedRealtimeEndpointQuotaScopeAccount, Limit: perAccount, Observed: accountCount}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	in.UpdatedAt = in.CreatedAt
	in.CallbackAuthTokenSealed = append([]byte(nil), in.CallbackAuthTokenSealed...)
	in.AuthTokenSealed = append([]byte(nil), in.AuthTokenSealed...)
	in.AuthAudience = append([]string(nil), in.AuthAudience...)
	in.AuthAlgorithms = append([]string(nil), in.AuthAlgorithms...)
	in.AuthRequiredClaims = cloneManagedRealtimeClaims(in.AuthRequiredClaims)
	in.AllowedOrigins = append([]string(nil), in.AllowedOrigins...)
	m.managedRealtimeEndpoints[in.ID] = in
	return in, nil
}

func (m *MemStore) ManagedRealtimeEndpointByID(_ context.Context, id string) (ManagedRealtimeEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.managedRealtimeEndpoints[id]
	if !ok {
		return ManagedRealtimeEndpoint{}, ErrNotFound
	}
	e.CallbackAuthTokenSealed = append([]byte(nil), e.CallbackAuthTokenSealed...)
	e.AuthTokenSealed = append([]byte(nil), e.AuthTokenSealed...)
	e.AuthAudience = append([]string(nil), e.AuthAudience...)
	e.AuthAlgorithms = append([]string(nil), e.AuthAlgorithms...)
	e.AuthRequiredClaims = cloneManagedRealtimeClaims(e.AuthRequiredClaims)
	e.AllowedOrigins = append([]string(nil), e.AllowedOrigins...)
	return e, nil
}

func (m *MemStore) UpdateManagedRealtimeEndpoint(_ context.Context, id string, p UpdateManagedRealtimeEndpointParams) (ManagedRealtimeEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.managedRealtimeEndpoints[id]
	if !ok {
		return ManagedRealtimeEndpoint{}, ErrNotFound
	}
	if p.CallbackURL != nil {
		e.CallbackURL = *p.CallbackURL
	}
	if p.ConnectPath != nil {
		e.ConnectPath = *p.ConnectPath
	}
	if p.MessagePath != nil {
		e.MessagePath = *p.MessagePath
	}
	if p.DisconnectPath != nil {
		e.DisconnectPath = *p.DisconnectPath
	}
	if p.CallbackAuthTokenSealed != nil {
		e.CallbackAuthTokenSealed = append([]byte(nil), (*p.CallbackAuthTokenSealed)...)
	}
	if p.AuthTokenSealed != nil {
		e.AuthTokenSealed = append([]byte(nil), (*p.AuthTokenSealed)...)
	}
	if p.AuthMode != nil {
		e.AuthMode = *p.AuthMode
	}
	if p.AuthIssuer != nil {
		e.AuthIssuer = *p.AuthIssuer
	}
	if p.AuthJWKSURL != nil {
		e.AuthJWKSURL = *p.AuthJWKSURL
	}
	if p.AuthAudience != nil {
		e.AuthAudience = append([]string(nil), (*p.AuthAudience)...)
	}
	if p.AuthAlgorithms != nil {
		e.AuthAlgorithms = append([]string(nil), (*p.AuthAlgorithms)...)
	}
	if p.AuthRequiredClaims != nil {
		e.AuthRequiredClaims = cloneManagedRealtimeClaims(*p.AuthRequiredClaims)
	}
	if p.AllowedOrigins != nil {
		e.AllowedOrigins = append([]string(nil), (*p.AllowedOrigins)...)
	}
	if p.MaxConnections != nil {
		e.MaxConnections = *p.MaxConnections
	}
	if p.MaxMessageBytes != nil {
		e.MaxMessageBytes = *p.MaxMessageBytes
	}
	if p.MaxConnectionAgeSeconds != nil {
		e.MaxConnectionAgeSeconds = *p.MaxConnectionAgeSeconds
	}
	if p.Enabled != nil {
		e.Enabled = *p.Enabled
	}
	e.UpdatedAt = time.Now()
	m.managedRealtimeEndpoints[id] = e
	return e, nil
}

func (m *MemStore) DeleteManagedRealtimeEndpoint(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[id]; !ok {
		return ErrNotFound
	}
	delete(m.managedRealtimeEndpoints, id)
	return nil
}

func (m *MemStore) ListManagedRealtimeEndpointsForApp(_ context.Context, appID string) ([]ManagedRealtimeEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return listManagedRealtimeEndpoints(m.managedRealtimeEndpoints, func(e ManagedRealtimeEndpoint) bool { return e.AppID == appID }), nil
}

func (m *MemStore) ListManagedRealtimeEndpointsForAccount(_ context.Context, accountID string) ([]ManagedRealtimeEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return listManagedRealtimeEndpoints(m.managedRealtimeEndpoints, func(e ManagedRealtimeEndpoint) bool { return e.AccountID == accountID }), nil
}

func (m *MemStore) ListManagedRealtimeEndpoints(_ context.Context) ([]ManagedRealtimeEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return listManagedRealtimeEndpoints(m.managedRealtimeEndpoints, func(ManagedRealtimeEndpoint) bool { return true }), nil
}

func listManagedRealtimeEndpoints(rows map[string]ManagedRealtimeEndpoint, keep func(ManagedRealtimeEndpoint) bool) []ManagedRealtimeEndpoint {
	out := make([]ManagedRealtimeEndpoint, 0, len(rows))
	for _, e := range rows {
		if keep(e) {
			e.CallbackAuthTokenSealed = append([]byte(nil), e.CallbackAuthTokenSealed...)
			e.AuthTokenSealed = append([]byte(nil), e.AuthTokenSealed...)
			e.AuthAudience = append([]string(nil), e.AuthAudience...)
			e.AuthAlgorithms = append([]string(nil), e.AuthAlgorithms...)
			e.AuthRequiredClaims = cloneManagedRealtimeClaims(e.AuthRequiredClaims)
			e.AllowedOrigins = append([]string(nil), e.AllowedOrigins...)
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func cloneManagedRealtimeClaims(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
