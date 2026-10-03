// Tenant surfaces — the multi-tenant hostname routing primitive
// introduced in ADR-100 (issue #879). One surface binds an account
// to a single app (D1, surfaced as NOT NULL app_id at the SQL layer
// — see migrations/00243_tenant_surfaces.sql app_or_not_chk) and
// groups N verified hostnames under one managed certificate. The
// cert engine (pkg/gateway/cert_issuer.go) re-mints on every surface
// mutation (D3) driven by a pg_notify subscriber in
// cmd/gatewayd-internal.
//
// Domain types live here so pkg/state/types.go stays a stable
// append-only surface; the Store interface additions live in
// pkg/state/store.go directly (matching the existing convention
// for CustomDomain).
package state

import (
	"errors"
	"fmt"
	"time"
)

// CertKind — what kind of cert this surface mints. v1 wires two
// values:
//
//   - per_host_san     v1 default; one cert, N hostnames as SANs
//     (up to the Let's Encrypt 100-SAN cap;
//     oversized sets fall back to per_host).
//   - shared_wildcard  schema accepts (forward-compat); the issuer
//     rejects with ErrUnsupportedCertKind until a
//     follow-up ADR ships the customer-zone DNS-01
//     solver.
//
// The list is closed via the surface CHECK constraint and the
// CertIssuer branch; a new value requires a migration.
type CertKind string

const (
	CertKindPerHostSAN     CertKind = "per_host_san"
	CertKindSharedWildcard CertKind = "shared_wildcard"
	// CertKindPerHost is the >MaxSANPerCert fallback shape
	// (ADR-100 §"Cert engine shape" line 76). One cert per
	// hostname in the verified set, no SAN bundling. The
	// wrapper at pkg/gateway/cert_issuer_tenant_surface.go
	// rejects this value today with a clear
	// "per-host bundler ships in follow-up ADR-114" last_error;
	// the constant lands in PR-D commit 5 so the schema
	// accepts it (migration 00284) and a follow-up ADR doesn't
	// need a schema-touching migration.
	CertKindPerHost CertKind = "per_host"
)

// SurfaceStatus — the lifecycle. Soft-delete only; pk stays
// stable so audit / cert_history rows can keep referencing the row.
type SurfaceStatus string

const (
	SurfaceStatusPending   SurfaceStatus = "pending"
	SurfaceStatusActive    SurfaceStatus = "active"
	SurfaceStatusSuspended SurfaceStatus = "suspended"
	SurfaceStatusDeleted   SurfaceStatus = "deleted"
)

// CertState — the certificate half of the lifecycle. Mirrors the
// apid's `tls_state`-ish field shape (string, not enum). `none` is
// the initial state; `pending` flips on as soon as the cert-remint
// goroutine picks up the surface for processing; `issued` lands on
// success; `failed` records the cert_last_error for operator + customer
// dashboards. Renewal re-runs the cycle from `pending`.
type CertState string

const (
	CertStateNone    CertState = "none"
	CertStatePending CertState = "pending"
	CertStateIssued  CertState = "issued"
	CertStateFailed  CertState = "failed"
)

// ValidCertKind reports whether k is one of the closed-set values
// enforced by the tenant_surfaces CHECK constraint. The pgstore
// scan path uses this to fail-loud on a row that bypassed the
// constraint (manual fix, replication drift) — a defensive read
// rather than letting the issuer silently fall through with an
// unknown kind.
func (k CertKind) Valid() bool {
	switch k {
	case CertKindPerHostSAN, CertKindSharedWildcard, CertKindPerHost:
		return true
	}
	return false
}

// ValidSurfaceStatus reports whether s is one of the closed-set
// values enforced by the tenant_surfaces CHECK constraint. Mirrors
// ValidCertKind; the scan path uses it as a defense-in-depth read.
func (s SurfaceStatus) Valid() bool {
	switch s {
	case SurfaceStatusPending, SurfaceStatusActive, SurfaceStatusSuspended, SurfaceStatusDeleted:
		return true
	}
	return false
}

// ValidCertState reports whether s is one of the closed-set values
// used by the cert engine. The tenant_surfaces table does not have
// a CHECK for cert_state (the column is open to future values), but
// the scan still asserts the v1 set so a stray value surfaces a
// log+metric event rather than a silent downstream mismatch.
func (s CertState) Valid() bool {
	switch s {
	case CertStateNone, CertStatePending, CertStateIssued, CertStateFailed:
		return true
	}
	return false
}

// TenantSurface — one row in tenant_surfaces.
type TenantSurface struct {
	ID            string
	AccountID     string
	AppID         string
	Name          string
	CertKind      CertKind
	Status        SurfaceStatus
	CertState     CertState
	CertNotAfter  time.Time
	CertLastError string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// PlatformTenantManaged is true only when an account owner's platform-tenant
	// bundle apply created this surface; linking an existing surface leaves it false.
	PlatformTenantManaged bool
}

// Active reports whether the surface is in a state where new
// requests should be routed to it. Soft-deleted and suspended
// surfaces are routed-around by pgRouter.ResolveHost (PR-B).
func (s TenantSurface) Active() bool { return s.Status == SurfaceStatusActive }

// CertValid reports whether the surface has an in-date cert at t.
// CertNotAfter.IsZero() indicates the surface has never been minted.
func (s TenantSurface) CertValid(t time.Time) bool {
	return !s.CertNotAfter.IsZero() && s.CertNotAfter.After(t)
}

// TenantHostname — one row in tenant_hostnames.
type TenantHostname struct {
	ID             string
	SurfaceID      string
	Hostname       string
	ChallengeToken string
	VerifiedAt     time.Time
	LastCheckAt    time.Time
	LastError      string
	CreatedAt      time.Time
	// PlatformTenantManaged is true only when an account owner's platform-tenant
	// bundle apply created this hostname.
	PlatformTenantManaged bool
}

// Verified — mirror of CustomDomain.Verified() — accessor used by
// CertIssuer.RequestCertForSurface to fail closed before SAN assembly.
func (h TenantHostname) Verified() bool { return !h.VerifiedAt.IsZero() }

// CreateTenantSurfaceParams — input to CreateTenantSurfaceIfUnderQuota.
// AppID must be non-empty (table-level CHECK enforces it; the
// apid handler validates before reaching the store).
type CreateTenantSurfaceParams struct {
	AccountID string
	AppID     string
	Name      string
	CertKind  CertKind
}

// CreateTenantHostnameParams — input to CreateTenantHostnameIfUnderQuota.
// Hostname and ChallengeToken are caller's responsibility to
// shape (lowercase, no scheme, RFC 1035 compliant); the store
// enforces the citext storage + the UQ on hostname alone.
type CreateTenantHostnameParams struct {
	SurfaceID      string
	Hostname       string
	ChallengeToken string
}

// UpdateSurfaceCertParams — input to UpdateTenantSurfaceCert.
// CertState values flow through the cert engine's own state
// machine; the store does not validate transitions (the engine
// owns that contract).
type UpdateSurfaceCertParams struct {
	SurfaceID string
	CertState CertState
	NotAfter  time.Time
	LastError string
}

// Quota errors. The shape mirrors EdgeRuleQuotaError (types.go:
// EdgeRuleQuotaError) so the apid handler can `errors.As` it
// uniformly. A separate *TenantHostnameQuotaError carries the
// SurfaceID so the customer-facing message can name the overflowing
// surface when the error surfaces.
type TenantSurfaceQuotaError struct {
	Limit    int
	Observed int
}

func (e *TenantSurfaceQuotaError) Error() string {
	return fmt.Sprintf("state: tenant surface quota exceeded (limit=%d, observed=%d)", e.Limit, e.Observed)
}

func (e *TenantSurfaceQuotaError) Is(target error) bool {
	_, ok := target.(*TenantSurfaceQuotaError)
	return ok
}

type TenantHostnameQuotaError struct {
	Limit     int
	Observed  int
	SurfaceID string
}

func (e *TenantHostnameQuotaError) Error() string {
	return fmt.Sprintf("state: tenant hostname quota exceeded (limit=%d, observed=%d, surface=%s)", e.Limit, e.Observed, e.SurfaceID)
}

func (e *TenantHostnameQuotaError) Is(target error) bool {
	_, ok := target.(*TenantHostnameQuotaError)
	return ok
}

// ErrTenantSurfacesNotAllowed — returned by CreateTenantSurfaceIfUnderQuota
// when limits.TenantSurfacesAllowed is false on the plan (Free today).
// Distinct from the quota error because the customer fix is "upgrade
// plan", not "delete a surface". Maps to the RFC 7807 code
// CodeTenantSurfacesNotAllowed at the apid handler.
var ErrTenantSurfacesNotAllowed = errors.New("state: tenant surfaces not allowed on plan")

// ErrTenantHostnameNotVerified — surface-issuance fail-closed case.
// CertIssuer.RequestCertForSurface returns this when the surface has
// no verified hostnames (an empty verified set, or every hostname
// in the set has VerifiedAt zero). Lives in pkg/state so the engine
// and the store agree on the sentinel identity.
var ErrTenantHostnameNotVerified = errors.New("state: tenant surface has no verified hostnames")

// ErrUnsupportedCertKind — typed sentinel returned when the
// cert_kind value on a surface is recognised by the schema
// (CertKind.Valid() returns true) but the issuer refuses to
// mint today. PR-D commit 5: the wrapper at
// pkg/gateway/cert_issuer_tenant_surface.go returns this for
// shared_wildcard (customer-zone DNS-01 deferred to ADR-114)
// and per_host (>MaxSANPerCert bundler deferred to ADR-114).
// Lives in pkg/state so the apid handler can errors.As this
// sentinel uniformly when the cert engine logs it back through
// cert_last_error — same shape as ErrTenantHostnameNotVerified
// and ErrTenantSurfacesNotAllowed.
var ErrUnsupportedCertKind = errors.New("state: cert_kind not mintable in v1")
