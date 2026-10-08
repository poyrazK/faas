package state

// adr: 773
import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

var ErrRegionalRuntimeConsent = errors.New("regional runtime delivery consent is unavailable")

type RegionalRuntimeConsent struct {
	Consent   runtimequota.Consent `json:"consent"`
	CreatedAt time.Time            `json:"created_at"`
	RevokedAt *time.Time           `json:"revoked_at,omitempty"`
}

type RegionalRuntimeConsentStore interface {
	PutRegionalRuntimeConsent(context.Context, runtimequota.Consent) (RegionalRuntimeConsent, error)
	RegionalRuntimeConsent(context.Context, string, string, string, string) (RegionalRuntimeConsent, error)
	RevokeRegionalRuntimeConsent(context.Context, string, string, string, string) (RegionalRuntimeConsent, error)
}

type RegionalRuntimeConsentLimitError struct{ Limit, Observed int64 }

func (*RegionalRuntimeConsentLimitError) Error() string {
	return "regional runtime consent bound reached"
}

func cloneRuntimeConsent(c RegionalRuntimeConsent) RegionalRuntimeConsent {
	if c.RevokedAt != nil {
		t := *c.RevokedAt
		c.RevokedAt = &t
	}
	return c
}

func runtimeConsentExpiry(c runtimequota.Consent, now time.Time) error {
	expiry := time.UnixMicro(c.ExpiresAtMicros)
	if !expiry.After(now) || expiry.After(now.Add(api.RegionalRuntimeConsentMaxLifetime)) {
		return ErrInvalidArgument
	}
	return nil
}

func runtimeConsentAllows(c RegionalRuntimeConsent, grant runtimequota.Grant, now time.Time) bool {
	return c.Consent.Validate() == nil && c.RevokedAt == nil && time.UnixMicro(c.Consent.ExpiresAtMicros).After(now) && c.Consent.Matches(grant)
}
