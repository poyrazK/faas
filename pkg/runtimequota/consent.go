package runtimequota

// adr: 773
import "github.com/onebox-faas/faas/pkg/api"

// Consent authorizes quota delivery for one original directory/app binding.
// It contains no credential and does not itself authorize runtime admission.
type Consent struct {
	ID              string  `json:"id"`
	IssuerID        string  `json:"issuer_id"`
	GlobalAppID     string  `json:"global_app_id"`
	OwnerID         string  `json:"owner_id"`
	Binding         Binding `json:"binding"`
	ExpiresAtMicros int64   `json:"expires_at_micros"`
}

func (c Consent) Validate() error {
	if !validID(c.ID) || !validID(c.IssuerID) || !validID(c.GlobalAppID) || !validID(c.OwnerID) ||
		c.Binding.Validate() != nil || c.Binding.RegionalOrgID != "" || c.ExpiresAtMicros <= 0 {
		return ErrInvalid
	}
	return nil
}

func DecodeConsent(raw []byte) (Consent, error) {
	var c Consent
	if decodeCanonicalObject(raw, &c, api.RegionalRuntimeConsentMaxBytes) != nil || c.Validate() != nil {
		return Consent{}, ErrInvalid
	}
	return c, nil
}

func (c Consent) Matches(g Grant) bool {
	return c.IssuerID == g.IssuerID && c.GlobalAppID == g.GlobalAppID && c.OwnerID == g.OwnerID && c.Binding == g.Binding
}
