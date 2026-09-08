// memstore_tenant_surface.go — MemStore implementations for the
// tenant surfaces Store interface (ADR-100 / issue #879). MemStore is
// the test-only in-process mirror of PgStore; every method synchronises
// against m.mu (MemStore is single-process; per-row FOR UPDATE is
// unnecessary because the lock is implicit). The two quota-check
// methods mirror CreateEdgeRuleIfUnderQuota (memstore.go:9580).
package state

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// CreateTenantSurfaceIfUnderQuota — same TOCTOU-defence shape as
// memstore.go:9580 CreateEdgeRuleIfUnderQuota: single m.mu.Lock()
// wraps the parent lookup + count + insert. There is no SoftDelete
// tombstoning; the in-memory map lacks the partial-unique predicate
// the SQL schema has, so duplicate (account_id, name) pairs are
// rejected with the same ErrConflict the pgstore returns (state-level
// invariant: a soft-deleted surface frees the name, so a re-create
// post-delete is allowed).
func (m *MemStore) CreateTenantSurfaceIfUnderQuota(_ context.Context, in CreateTenantSurfaceParams, limits api.Limits) (TenantSurface, error) {
	if !limits.TenantSurfacesAllowed {
		return TenantSurface{}, ErrTenantSurfacesNotAllowed
	}
	if in.CertKind == "" {
		in.CertKind = CertKindPerHostSAN
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	acct, ok := m.accounts[in.AccountID]
	if !ok || acct.Status == AccountDeletedPending {
		return TenantSurface{}, ErrNotFound
	}
	observed := 0
	for _, s := range m.tenantSurfaces {
		if s.AccountID == in.AccountID && s.Status != SurfaceStatusDeleted {
			observed++
		}
	}
	if observed >= limits.TenantSurfacesPerAccount {
		return TenantSurface{}, &TenantSurfaceQuotaError{
			Limit:    limits.TenantSurfacesPerAccount,
			Observed: observed,
		}
	}
	for _, s := range m.tenantSurfaces {
		if s.AccountID == in.AccountID && s.Name == in.Name && s.Status != SurfaceStatusDeleted {
			return TenantSurface{}, ErrConflict
		}
	}
	// PostgreSQL's timestamp ordering is stable for sequential inserts, but
	// time.Now().UTC() can have equal wall-clock values on this in-memory
	// path. Keep the test double's CreatedAt order deterministic as well.
	var latestCreatedAt time.Time
	for _, existing := range m.tenantSurfaces {
		if existing.CreatedAt.After(latestCreatedAt) {
			latestCreatedAt = existing.CreatedAt
		}
	}
	now := nextTenantSurfaceTime(latestCreatedAt)
	surf := TenantSurface{
		ID:        uuid.NewString(),
		AccountID: in.AccountID,
		AppID:     in.AppID,
		Name:      in.Name,
		CertKind:  in.CertKind,
		Status:    SurfaceStatusPending,
		CertState: CertStateNone,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.tenantSurfaces[surf.ID] = surf
	return surf, nil
}

// GetTenantSurfaceByID — direct map lookup.
func (m *MemStore) GetTenantSurfaceByID(_ context.Context, id string) (TenantSurface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.tenantSurfaces[id]
	if !ok {
		return TenantSurface{}, ErrNotFound
	}
	return s, nil
}

// GetTenantSurfaceByName — linear scan; dataset is bounded by the
// per-account quota (max 25 today) so the cost is trivial.
func (m *MemStore) GetTenantSurfaceByName(_ context.Context, accountID, name string) (TenantSurface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.tenantSurfaces {
		if s.AccountID == accountID && s.Name == name {
			return s, nil
		}
	}
	return TenantSurface{}, ErrNotFound
}

// ListTenantSurfacesForAccount — sorted by CreatedAt then ID; soft
// deletes are filtered out so the consumer sees the same shape
// pgstore returns.
func (m *MemStore) ListTenantSurfacesForAccount(_ context.Context, accountID string) ([]TenantSurface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TenantSurface, 0)
	for _, s := range m.tenantSurfaces {
		if s.AccountID == accountID && s.Status != SurfaceStatusDeleted {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// ListTenantSurfacesForApp — required by the pgRouter.ResolveHost
// inverse path.
func (m *MemStore) ListTenantSurfacesForApp(_ context.Context, appID string) ([]TenantSurface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TenantSurface, 0)
	for _, s := range m.tenantSurfaces {
		if s.AppID == appID && s.Status != SurfaceStatusDeleted {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// CountTenantSurfacesForAccount — quota + dashboard hot path.
func (m *MemStore) CountTenantSurfacesForAccount(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.tenantSurfaces {
		if s.AccountID == accountID && s.Status != SurfaceStatusDeleted {
			n++
		}
	}
	return n, nil
}

// UpdateTenantSurfaceStatus — mirrors the status flip but doesn't
// touch updated_at at the time.Now() level; we update it so the
// (apiserver) audit + dashboard see the change.
func (m *MemStore) UpdateTenantSurfaceStatus(_ context.Context, id string, status SurfaceStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.tenantSurfaces[id]
	if !ok {
		return ErrNotFound
	}
	s.Status = status
	s.UpdatedAt = nextTenantSurfaceTime(s.UpdatedAt)
	m.tenantSurfaces[id] = s
	return nil
}

// UpdateTenantSurfaceCert — the in-memory cert state transition.
//
// PR-D code review (PR #959 candidate 5): the in-memory twin
// mirrors the PgStore's "no-op when cert_state already matches"
// invariant so unit tests that exercise the wrapper's
// self-recursive-notify-storm suppression see the same shape
// as production. The notify storm is a Postgres-specific
// concern (the trigger fires on actual UPDATE) but the
// semantic — don't write when the state already matches — is
// universally correct and keeps the test fixtures honest.
func (m *MemStore) UpdateTenantSurfaceCert(_ context.Context, in UpdateSurfaceCertParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.tenantSurfaces[in.SurfaceID]
	if !ok {
		return ErrNotFound
	}
	if s.CertState == in.CertState {
		// No-op: the row's cert_state already matches the
		// requested value. Writes are skipped so the
		// production pg_notify trigger doesn't fire on
		// idempotent re-entries.
		return nil
	}
	s.CertState = in.CertState
	s.CertNotAfter = in.NotAfter
	s.CertLastError = in.LastError
	s.UpdatedAt = nextTenantSurfaceTime(s.UpdatedAt)
	m.tenantSurfaces[in.SurfaceID] = s
	return nil
}

// DeleteTenantSurface — soft delete; the row stays for audit /
// cert_history paths. Context is forwarded to the underlying
// UpdateSurfaceStatus so client cancellation / deadline-exceeded
// is respected (PgStore sibling forwards ctx — the MemStore
// previously discarded it, masking the cancellation in tests).
func (m *MemStore) DeleteTenantSurface(ctx context.Context, id string) error {
	return m.UpdateTenantSurfaceStatus(ctx, id, SurfaceStatusDeleted)
}

// ListTenantSurfacesNearingExpiry — renewer hot-path (PR-D
// commit 3). Mirrors the pgstore predicate
// (status='active' AND cert_state='issued' AND
// cert_not_after < cutoff); sort-by-cert-not-after ascending so
// the renewer hits the most-overdue surface first. Returns an
// empty slice when the dataset has no renewals due.
//
// PR-D code review (PR #959 candidate 6): the renewer now
// pages through the result set with limit + keyset cursor
// (cert_not_after, id) so a single tick can't enqueue
// unbounded UPDATEs after a CA outage. The in-memory twin
// matches the same predicate + secondary sort so unit tests
// that exercise the renewer's pagination see the same shape.
func (m *MemStore) ListTenantSurfacesNearingExpiry(_ context.Context, cutoff time.Time, limit int, afterCertNotAfter time.Time, afterID string) ([]TenantSurface, error) {
	if limit <= 0 {
		limit = 1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TenantSurface, 0)
	for _, s := range m.tenantSurfaces {
		if s.Status != SurfaceStatusActive {
			continue
		}
		if s.CertState != CertStateIssued {
			continue
		}
		if !s.CertNotAfter.Before(cutoff) {
			continue
		}
		// Keyset cursor: skip rows whose (cert_not_after, id)
		// is at-or-before the cursor. The strict > mirrors
		// the pgstore predicate.
		if afterID != "" {
			if s.CertNotAfter.Before(afterCertNotAfter) {
				continue
			}
			if s.CertNotAfter.Equal(afterCertNotAfter) && s.ID <= afterID {
				continue
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CertNotAfter.Equal(out[j].CertNotAfter) {
			return out[i].ID < out[j].ID
		}
		return out[i].CertNotAfter.Before(out[j].CertNotAfter)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// TouchTenantSurfaceForRenewal — bumps updated_at so the
// tenant_surface_changed notify trigger fires; the renewer
// rides the existing pipeline instead of duplicating it.
// Mirrors pgstore's pgxpool.Exec.
func (m *MemStore) TouchTenantSurfaceForRenewal(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.tenantSurfaces[id]
	if !ok {
		return ErrNotFound
	}
	s.UpdatedAt = nextTenantSurfaceTime(s.UpdatedAt)
	m.tenantSurfaces[id] = s
	return nil
}

// nextTenantSurfaceTime preserves strict in-memory ordering when the host
// clock returns the same wall-clock instant for two consecutive operations.
// The database is the production source of timestamps; this only keeps the
// MemStore twin deterministic and faithful to ORDER BY created_at/updated_at.
func nextTenantSurfaceTime(previous time.Time) time.Time {
	now := time.Now().UTC()
	if !previous.IsZero() && !now.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return now
}

// TenantSurfaceByHostname — pgRouter.ResolveHost hot path; linear
// scan over hostnames. Dataset is bounded by (surfaces per acct) ×
// (hostnames per surface) = 25*250 = 6250 worst case, well under the
// ms-bound latency budget. Lookup returns the parent surface, same
// shape as DomainByName / custom_domains.
func (m *MemStore) TenantSurfaceByHostname(_ context.Context, hostname string) (TenantSurface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.tenantHostnames {
		if h.Hostname == hostname {
			if s, ok := m.tenantSurfaces[h.SurfaceID]; ok && s.Status != SurfaceStatusDeleted {
				return s, nil
			}
			return TenantSurface{}, ErrNotFound
		}
	}
	return TenantSurface{}, ErrNotFound
}

// CreateTenantHostnameIfUnderQuota — locks on the parent surface (m.mu
// here is process-wide), counts, enforces the UQ on hostname, inserts.
func (m *MemStore) CreateTenantHostnameIfUnderQuota(_ context.Context, in CreateTenantHostnameParams, limits api.Limits) (TenantHostname, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.tenantSurfaces[in.SurfaceID]
	if !ok || s.Status == SurfaceStatusDeleted {
		return TenantHostname{}, ErrNotFound
	}
	observed := 0
	// Per-surface cap counts VERIFIED hostnames only. See
	// pgstore_tenant_surface.go:CreateTenantHostnameIfUnderQuota
	// for the rationale (verified-only floors the customer out of
	// a lock-by-unverified-tail; the doc at limits.go:449-450 says
	// "verified hostnames one surface may hold").
	for _, h := range m.tenantHostnames {
		if h.SurfaceID == in.SurfaceID && h.Verified() {
			observed++
		}
	}
	if observed >= limits.TenantHostnamesPerSurface {
		return TenantHostname{}, &TenantHostnameQuotaError{
			Limit:     limits.TenantHostnamesPerSurface,
			Observed:  observed,
			SurfaceID: in.SurfaceID,
		}
	}
	if _, exists := m.tenantHostnames[in.Hostname]; exists {
		return TenantHostname{}, ErrConflict
	}
	// Case-insensitive collision check mirrors the schema's
	// tenant_hostnames.hostname citext column. PgStore rejects
	// 'Customer.com' vs 'customer.com' via the unique index; the
	// memstore must do the same so tests that exercise case
	// variants converge on the same error.
	if _, exists := m.tenantHostnames[strings.ToLower(in.Hostname)]; exists {
		return TenantHostname{}, ErrConflict
	}
	now := time.Now().UTC()
	h := TenantHostname{
		ID:             uuid.NewString(),
		SurfaceID:      in.SurfaceID,
		Hostname:       in.Hostname,
		ChallengeToken: in.ChallengeToken,
		CreatedAt:      now,
	}
	m.tenantHostnames[h.Hostname] = h
	m.tenantHostnames[strings.ToLower(h.Hostname)] = h
	return h, nil
}

// ListTenantHostnamesForSurface — sorted by hostname for parity with
// the pgstore ORDER BY.
func (m *MemStore) ListTenantHostnamesForSurface(_ context.Context, surfaceID string) ([]TenantHostname, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TenantHostname, 0)
	for _, h := range m.tenantHostnames {
		if h.SurfaceID == surfaceID {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out, nil
}

// ListTenantSurfaceHostnames returns every hostname reserved by a non-deleted
// tenant surface. It is the global overlap read used by F4 wildcard creation.
func (m *MemStore) ListTenantSurfaceHostnames(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	active := make(map[string]struct{})
	for _, surface := range m.tenantSurfaces {
		if surface.Status != SurfaceStatusDeleted {
			active[surface.ID] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, hostname := range m.tenantHostnames {
		if _, ok := active[hostname.SurfaceID]; ok {
			key := strings.ToLower(hostname.Hostname)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, hostname.Hostname)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListVerifiedTenantHostnamesForSurface — SAN-assembly hot path.
func (m *MemStore) ListVerifiedTenantHostnamesForSurface(_ context.Context, surfaceID string) ([]TenantHostname, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TenantHostname, 0)
	for _, h := range m.tenantHostnames {
		if h.SurfaceID == surfaceID && h.Verified() {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out, nil
}

// CountTenantHostnamesForSurface.
func (m *MemStore) CountTenantHostnamesForSurface(_ context.Context, surfaceID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, h := range m.tenantHostnames {
		if h.SurfaceID == surfaceID {
			n++
		}
	}
	return n, nil
}

// MarkTenantHostnameVerified — sets VerifiedAt + LastCheckAt, clears
// LastError.
func (m *MemStore) MarkTenantHostnameVerified(_ context.Context, hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.tenantHostnames[hostname]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	h.VerifiedAt = now
	h.LastCheckAt = now
	h.LastError = ""
	m.tenantHostnames[hostname] = h
	return nil
}

// MarkTenantHostnameCheckFailed — dns_poller path; preserves
// VerifiedAt across a transient DNS failure.
func (m *MemStore) MarkTenantHostnameCheckFailed(_ context.Context, hostname, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.tenantHostnames[hostname]
	if !ok {
		return ErrNotFound
	}
	h.LastCheckAt = time.Now().UTC()
	h.LastError = reason
	m.tenantHostnames[hostname] = h
	return nil
}

// ListPendingTenantHostnames — dns_poller queue. Bounded by limit
// (default 50). Rows with LastCheckAt.IsZero() are always eligible;
// older rows come first.
func (m *MemStore) ListPendingTenantHostnames(_ context.Context, olderThan time.Time, limit int) ([]TenantHostname, error) {
	if limit <= 0 {
		limit = 50
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TenantHostname, 0)
	for _, h := range m.tenantHostnames {
		if h.Verified() {
			continue
		}
		if !h.LastCheckAt.IsZero() && !h.LastCheckAt.Before(olderThan) {
			continue
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		ti := out[i].LastCheckAt
		tj := out[j].LastCheckAt
		if ti.IsZero() && !tj.IsZero() {
			return true
		}
		if !ti.IsZero() && tj.IsZero() {
			return false
		}
		if ti.Equal(tj) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return ti.Before(tj)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DeleteTenantHostname — for tests + the apid path.
func (m *MemStore) DeleteTenantHostname(_ context.Context, hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tenantHostnames[hostname]; !ok {
		return ErrNotFound
	}
	// Mirror the case-insensitive indexing we wrote under
	// CreateTenantHostnameIfUnderQuota. Mcase variants of the
	// same row coexist in the map and both must be removed.
	delete(m.tenantHostnames, hostname)
	delete(m.tenantHostnames, strings.ToLower(hostname))
	return nil
}

// GetTenantHostnameByName — pgRouter.ResolveHost's tenant-surface
// branch uses this to fail closed on hostname.Verified() == false.
// Mirrors the citext storage contract the SQL schema enforces:
// callers pass the canonical lowercase form, but the in-memory map
// also indexes the lowercased form so a casing typo from the
// caller side doesn't return ErrNotFound in tests. Soft-deleted
// parent surfaces (status='deleted') are filtered out, matching
// the SQL JOIN's `s.status <> 'deleted'` predicate.
func (m *MemStore) GetTenantHostnameByName(_ context.Context, hostname string) (TenantHostname, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.tenantHostnames[hostname]
	if !ok {
		h, ok = m.tenantHostnames[strings.ToLower(hostname)]
	}
	if !ok {
		return TenantHostname{}, ErrNotFound
	}
	s, ok := m.tenantSurfaces[h.SurfaceID]
	if !ok || s.Status == SurfaceStatusDeleted {
		return TenantHostname{}, ErrNotFound
	}
	return h, nil
}

// errors.Is is used by tests for *TenantSurfaceQuotaError +
// *TenantHostnameQuotaError assertions; keep the symbol live so the
// blank import doesn't get stripped from the editor view.
var _ = errors.Is
