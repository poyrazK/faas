package triggerconfig

import (
	"encoding/json"
	"errors"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
)

// ResealStatus is what Reseal found in one stored trigger config.
type ResealStatus int

const (
	// ResealWrite: a credential still opens with a legacy identity or is
	// stored in plaintext; Reseal returned the config sealed to the domain
	// alone.
	ResealWrite ResealStatus = iota
	// ResealCurrent: every credential opens with the domain identity alone,
	// or the config holds none.
	ResealCurrent
	// ResealFailed: a credential opens with no identity given.
	ResealFailed
)

// Reseal re-seals every credential of a stored trigger config (Kafka SASL
// password and TLS client key, Redis password, NATS URL userinfo) to the
// delivery seal domain alone (ADR 20261022111812, step 3). legacy are the
// fleet and host identities. A credential stored in plaintext before
// sealing is sealed too. Only ResealWrite returns a config to store.
func Reseal(kind api.TriggerKind, raw json.RawMessage, domain *age.X25519Identity, legacy []*age.X25519Identity) (json.RawMessage, ResealStatus, error) {
	if domain == nil {
		return nil, ResealFailed, errors.New("triggerconfig: reseal needs the domain identity")
	}
	_, plainErr := Seal(kind, raw, nil)
	hasPlain := errors.Is(plainErr, ErrRecipientUnavailable)
	if plainErr != nil && !hasPlain {
		return nil, ResealFailed, nil
	}
	if !hasPlain {
		// Opening with no identity succeeds only when nothing is sealed.
		if _, err := Open(kind, raw, nil); err == nil {
			return nil, ResealCurrent, nil
		}
		_, domainErr := Open(kind, raw, []*age.X25519Identity{domain})
		_, legacyErr := Open(kind, raw, legacy)
		if domainErr == nil && (len(legacy) == 0 || legacyErr != nil) {
			return nil, ResealCurrent, nil
		}
	}
	opened, err := Open(kind, raw, append([]*age.X25519Identity{domain}, legacy...))
	if err != nil {
		return nil, ResealFailed, nil
	}
	sealed, err := Seal(kind, opened, []*age.X25519Recipient{domain.Recipient()})
	if err != nil {
		return nil, ResealFailed, err
	}
	return sealed, ResealWrite, nil
}
