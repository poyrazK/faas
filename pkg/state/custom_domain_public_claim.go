package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type publicCustomDomainClaimKey struct{}

// WithPublicCustomDomainClaim requires the public API's wildcard ownership
// and tenant-hostname checks inside publication, including DNS-proof takeovers.
// Internal repair operations retain their existing ability to publish shadows.
func WithPublicCustomDomainClaim(ctx context.Context) context.Context {
	return context.WithValue(ctx, publicCustomDomainClaimKey{}, true)
}

// WithPublicTenantHostnameClaim repeats the public API's hostname exclusions
// after acquiring the same publication locks as custom-domain creation.
func WithPublicTenantHostnameClaim(ctx context.Context) context.Context {
	return WithPublicCustomDomainClaim(ctx)
}

func publicCustomDomainClaim(ctx context.Context) bool {
	checked, _ := ctx.Value(publicCustomDomainClaimKey{}).(bool)
	return checked
}

type CustomDomainWildcardConflictError struct {
	Domain, Wildcard string
}

func (e *CustomDomainWildcardConflictError) Error() string {
	return fmt.Sprintf("%s is below the verified wildcard domain %s, which belongs to another account or organization", e.Domain, e.Wildcard)
}
func (e *CustomDomainWildcardConflictError) Unwrap() error { return ErrConflict }

// CustomDomainTenantOverlapError identifies the two registries whose public
// reservations must remain disjoint, regardless of which writer wins the lock.
type CustomDomainTenantOverlapError struct {
	Wildcard, Hostname string
}

func (e *CustomDomainTenantOverlapError) Error() string {
	return fmt.Sprintf("wildcard %s overlaps tenant hostname %s", e.Wildcard, e.Hostname)
}
func (e *CustomDomainTenantOverlapError) Unwrap() error { return ErrConflict }

func checkPublicCustomDomainClaim(ctx context.Context, tx pgx.Tx, domain, appID string) error {
	if !publicCustomDomainClaim(ctx) {
		return nil
	}
	if suffix, wildcard := WildcardDomainSuffix(domain); wildcard {
		hostname, err := sqlc.New().FindCustomDomainTenantOverlap(ctx, tx, suffix)
		if err == nil {
			return &CustomDomainTenantOverlapError{Wildcard: domain, Hostname: hostname}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check tenant hostname overlap: %w", err)
		}
	}
	base := strings.TrimPrefix(strings.ToLower(domain), "*.")
	var ancestors []string
	for {
		_, parent, ok := strings.Cut(base, ".")
		if !ok || !strings.Contains(parent, ".") {
			break
		}
		ancestors = append(ancestors, "*."+parent)
		base = parent
	}
	if len(ancestors) == 0 {
		return nil
	}
	wildcard, err := sqlc.New().FindForeignVerifiedWildcardForClaim(ctx, tx, sqlc.FindForeignVerifiedWildcardForClaimParams{Names: ancestors, AppID: appID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check public custom-domain claim: %w", err)
	}
	return &CustomDomainWildcardConflictError{Domain: domain, Wildcard: wildcard}
}

type TenantHostnameCustomDomainConflictError struct{ Hostname string }

func (e *TenantHostnameCustomDomainConflictError) Error() string {
	return fmt.Sprintf("hostname %s is a verified custom domain belonging to another account or organization", e.Hostname)
}
func (e *TenantHostnameCustomDomainConflictError) Unwrap() error { return ErrConflict }

func checkPublicTenantHostnameClaim(ctx context.Context, tx pgx.Tx, hostname, appID string) error {
	if !publicCustomDomainClaim(ctx) {
		return nil
	}
	_, err := sqlc.New().FindForeignVerifiedDomainForTenantHostname(ctx, tx, sqlc.FindForeignVerifiedDomainForTenantHostnameParams{Hostname: hostname, AppID: appID})
	if err == nil {
		return &TenantHostnameCustomDomainConflictError{Hostname: hostname}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check exact custom-domain overlap: %w", err)
	}
	var names []string
	for base := hostname; ; {
		_, parent, ok := strings.Cut(base, ".")
		if !ok || !strings.Contains(parent, ".") {
			break
		}
		names = append(names, "*."+parent)
		base = parent
	}
	wildcard, err := sqlc.New().FindTenantHostnameWildcardOverlap(ctx, tx, names)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check wildcard overlap: %w", err)
	}
	return &CustomDomainTenantOverlapError{Wildcard: wildcard, Hostname: hostname}
}

func (m *MemStore) checkPublicCustomDomainTenantOverlapLocked(ctx context.Context, domain string) error {
	if !publicCustomDomainClaim(ctx) || !IsWildcardCustomDomain(domain) {
		return nil
	}
	var hostname string
	for _, h := range m.tenantHostnames {
		if m.tenantSurfaces[h.SurfaceID].Status != SurfaceStatusDeleted && WildcardMatchesHost(domain, h.Hostname) && (hostname == "" || h.Hostname < hostname) {
			hostname = h.Hostname
		}
	}
	if hostname != "" {
		return &CustomDomainTenantOverlapError{Wildcard: domain, Hostname: hostname}
	}
	return nil
}

func (m *MemStore) checkPublicTenantHostnameClaimLocked(ctx context.Context, hostname, appID string) error {
	if !publicCustomDomainClaim(ctx) {
		return nil
	}
	reader := lockedCustomDomainReader{store: m}
	for _, d := range m.domains {
		if !strings.EqualFold(d.Domain, hostname) || !d.Verified() {
			continue
		}
		holder, holderOK := m.apps[d.AppID]
		claimant, claimantOK := m.apps[appID]
		if holderOK && claimantOK {
			owner, err := AppTenantID(ctx, reader, holder)
			if err != nil {
				return err
			}
			tenant, err := AppTenantID(ctx, reader, claimant)
			if err != nil {
				return err
			}
			if owner == tenant {
				continue
			}
		}
		return &TenantHostnameCustomDomainConflictError{Hostname: hostname}
	}
	var best CustomDomain
	for _, d := range m.domains {
		if d.ClaimLive(time.Now()) && WildcardMatchesHost(d.Domain, hostname) && (len(d.Domain) > len(best.Domain) || len(d.Domain) == len(best.Domain) && d.Domain < best.Domain) {
			best = d
		}
	}
	if best.Domain != "" {
		return &CustomDomainTenantOverlapError{Wildcard: best.Domain, Hostname: hostname}
	}
	return nil
}

// These reads run while MemStore's caller holds its mutex. Reusing the
// ownership policy keeps personal organizations and shared tenants consistent.
type lockedCustomDomainReader struct{ store *MemStore }

func (r lockedCustomDomainReader) DomainByName(_ context.Context, name string) (CustomDomain, error) {
	if d, ok := r.store.domains[name]; ok {
		return d, nil
	}
	return CustomDomain{}, ErrNotFound
}
func (r lockedCustomDomainReader) AppByID(_ context.Context, id string) (App, error) {
	if app, ok := r.store.apps[id]; ok {
		return app, nil
	}
	return App{}, ErrNotFound
}
func (r lockedCustomDomainReader) OrgByID(_ context.Context, id string) (Org, error) {
	if org, ok := r.store.orgs[id]; ok {
		return org, nil
	}
	return Org{}, ErrNotFound
}
