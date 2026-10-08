package globaldirectory

// adr: 774
import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	RuntimeConsentPending  = "pending"
	RuntimeConsentActive   = "active"
	RuntimeConsentRevoking = "revoking"
	RuntimeConsentRevoked  = "revoked"
)

var ErrRuntimeConsentUnavailable = errors.New("directory runtime consent unavailable")

type RuntimeConsentLimitError struct{ Limit, Observed int64 }

func (*RuntimeConsentLimitError) Error() string { return "directory runtime consent bound reached" }

// RuntimeConsentRecord retains public, purpose-specific authority. It never
// contains a customer credential and survives removal of its original target.
type RuntimeConsentRecord struct {
	Consent           runtimequota.Consent
	OwnerID           string
	Slug              string
	CreatedAt         time.Time
	Status            string
	RegionalCreatedAt *time.Time
	RegionalRevokedAt *time.Time
}

type RuntimeConsentStore interface {
	PrepareRuntimeConsent(context.Context, string, string, runtimequota.Consent) (RuntimeConsentRecord, error)
	RuntimeConsent(context.Context, string, string, string) (RuntimeConsentRecord, error)
	CheckRuntimeConsent(context.Context, string, string, string) (RuntimeConsentRecord, error)
	ObserveRuntimeConsent(context.Context, string, string, string, state.RegionalRuntimeConsent) (RuntimeConsentRecord, error)
	BeginRevokeRuntimeConsent(context.Context, string, string, string) (RuntimeConsentRecord, error)
}

func runtimeConsentOwner(raw string) string {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return ""
	}
	return id.String()
}

func runtimeConsentBinding(t Target) runtimequota.Binding {
	return runtimequota.Binding{CellID: t.CellID, Region: t.Region, CellGeneration: t.CellGeneration,
		RegionalAccountID: t.RegionalAccountID, RegionalOrgID: t.RegionalOrgID,
		RegionalAppID: t.RegionalAppID, RegionalAppSlug: t.RegionalAppSlug, APIBase: t.APIBase}
}

func copyRuntimeConsent(r RuntimeConsentRecord) RuntimeConsentRecord {
	clone := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		c := *t
		return &c
	}
	r.RegionalCreatedAt, r.RegionalRevokedAt = clone(r.RegionalCreatedAt), clone(r.RegionalRevokedAt)
	return r
}

func (r RuntimeConsentRecord) setupAllowed(now time.Time) bool {
	return (r.Status == RuntimeConsentPending || r.Status == RuntimeConsentActive) && time.UnixMicro(r.Consent.ExpiresAtMicros).After(now)
}

func (r RuntimeConsentRecord) observe(meta state.RegionalRuntimeConsent) (RuntimeConsentRecord, error) {
	if meta.Consent != r.Consent || meta.CreatedAt.IsZero() || !meta.CreatedAt.Before(time.UnixMicro(r.Consent.ExpiresAtMicros)) ||
		(r.RegionalCreatedAt != nil && !r.RegionalCreatedAt.Equal(meta.CreatedAt)) ||
		(meta.RevokedAt != nil && meta.RevokedAt.Before(meta.CreatedAt)) {
		return r, ErrConflict
	}
	if r.RegionalRevokedAt != nil && (meta.RevokedAt == nil || !r.RegionalRevokedAt.Equal(*meta.RevokedAt)) {
		return r, ErrConflict
	}
	if meta.RevokedAt == nil && !r.setupAllowed(time.Now()) {
		return r, ErrRuntimeConsentUnavailable
	}
	stamp := meta.CreatedAt
	r.RegionalCreatedAt = &stamp
	if meta.RevokedAt != nil {
		r.Status = RuntimeConsentRevoked
		stamp := *meta.RevokedAt
		r.RegionalRevokedAt = &stamp
	} else {
		r.Status = RuntimeConsentActive
	}
	return copyRuntimeConsent(r), nil
}
