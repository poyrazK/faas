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
	// Opening with no identity succeeds only when nothing is sealed.
	if !hasPlain && (opens(kind, raw, nil) ||
		opens(kind, raw, []*age.X25519Identity{domain}) && (len(legacy) == 0 || !opens(kind, raw, legacy))) {
		return nil, ResealCurrent, nil
	}
	all := append([]*age.X25519Identity{domain}, legacy...)
	if !opens(kind, raw, all) {
		return nil, ResealFailed, nil
	}
	opened, err := Open(kind, raw, all)
	if err != nil {
		return nil, ResealFailed, err
	}
	sealed, err := Seal(kind, opened, []*age.X25519Recipient{domain.Recipient()})
	if err != nil {
		return nil, ResealFailed, err
	}
	return sealed, ResealWrite, nil
}

// opens reports whether every sealed leaf of raw opens with ids.
func opens(kind api.TriggerKind, raw json.RawMessage, ids []*age.X25519Identity) bool {
	_, err := Open(kind, raw, ids)
	return err == nil
}
