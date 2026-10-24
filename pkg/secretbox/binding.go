package secretbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
)

// Binding names the account a runtime secret envelope belongs to
// (ADR-20261023173624). The age header says who can open an envelope; the
// binding, sealed inside it, says which tenant's workload it may be opened
// for. vmmd refuses a bound envelope for any other account, so ciphertext
// copied into another tenant's row cannot be decrypted into that tenant's VM.
// The zero Binding is an unbound envelope in the original format.
type Binding struct {
	AccountID string `json:"account_id"`
}

// ErrBindingMismatch is returned by Binding.Check for an envelope bound to
// an account other than the one it is being opened for.
var ErrBindingMismatch = errors.New("secretbox: envelope is bound to another account")

// ErrUnbound is returned by Binding.Require for an envelope that carries no
// binding.
var ErrUnbound = errors.New("secretbox: envelope is not bound to an account")

var bindingAccountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// IsZero reports whether b is the binding of an unbound envelope.
func (b Binding) IsZero() bool { return b == Binding{} }

func (b Binding) validate() error {
	if !bindingAccountPattern.MatchString(b.AccountID) {
		return fmt.Errorf("secretbox: binding account %q is not an account id", b.AccountID)
	}
	return nil
}

// Check reports whether an envelope carrying b may be opened for accountID.
// Unbound envelopes pass: rows sealed before binding stay readable until
// they are re-sealed. A bound envelope needs the same, non-empty account.
func (b Binding) Check(accountID string) error {
	if b.IsZero() {
		return nil
	}
	if accountID == "" || b.AccountID != accountID {
		return ErrBindingMismatch
	}
	return nil
}

// Require is Check that also refuses an unbound envelope, for readers that
// run after every stored envelope was bound (the contract step).
func (b Binding) Require(accountID string) error {
	if b.IsZero() {
		return ErrUnbound
	}
	return b.Check(accountID)
}

// boundEnvelopeVersion marks the bound plaintext. A pre-binding reader
// decodes plaintext into map[string]string, which fails on this number, so
// the previous release refuses a bound envelope instead of misreading it. A
// legacy envelope cannot carry the field: keys are upper-case env names.
const boundEnvelopeVersion = 1

const boundVersionField = "v"

type boundEnvelope struct {
	Version int      `json:"v"`
	Binding Binding  `json:"binding"`
	Values  Envelope `json:"values"`
}

// SealBound is SealTo for an envelope bound to binding's account. A zero
// binding writes the unbound format, byte-compatible with SealTo, so writers
// can thread a binding that is switched off.
func SealBound(recipients []*age.X25519Recipient, binding Binding, env Envelope) ([]byte, error) {
	if binding.IsZero() {
		return SealTo(recipients, env)
	}
	if err := binding.validate(); err != nil {
		return nil, err
	}
	if err := env.Validate(); err != nil {
		return nil, err
	}
	if env == nil {
		env = Envelope{}
	}
	plaintext, err := json.Marshal(boundEnvelope{Version: boundEnvelopeVersion, Binding: binding, Values: env})
	if err != nil {
		return nil, fmt.Errorf("secretbox: marshal bound envelope: %w", err)
	}
	return sealPlaintext(recipients, plaintext)
}

// SealOneBound is SealOne for an envelope bound to binding's account.
func SealOneBound(recipient *age.X25519Recipient, binding Binding, key, value string, maxValueBytes int) ([]byte, error) {
	if recipient == nil {
		return nil, errors.New("secretbox: nil recipient")
	}
	if maxValueBytes > 0 && len(value) > maxValueBytes {
		return nil, api.ErrSecretValueTooLarge(api.Limits{SecretValueMaxBytes: maxValueBytes}, len(value))
	}
	return SealBound([]*age.X25519Recipient{recipient}, binding, Envelope{key: value})
}

// OpenMultiBound is OpenMulti that also returns the envelope's binding, the
// zero Binding for an unbound envelope. Callers that decrypt for a workload
// must Check it; re-sealers must pass it back to SealBound.
func OpenMultiBound(identities []*age.X25519Identity, blob []byte) (Envelope, Binding, error) {
	raw, err := openPlaintext(identities, blob)
	if err != nil {
		return nil, Binding{}, err
	}
	return decodeEnvelope(raw)
}

// decodeEnvelope parses either plaintext format.
func decodeEnvelope(raw []byte) (Envelope, Binding, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, Binding{}, fmt.Errorf("secretbox: unmarshal envelope: %w", err)
	}
	if _, bound := fields[boundVersionField]; !bound {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, Binding{}, fmt.Errorf("secretbox: unmarshal envelope: %w", err)
		}
		if err := env.Validate(); err != nil {
			return nil, Binding{}, err
		}
		return env, Binding{}, nil
	}
	var b boundEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return nil, Binding{}, fmt.Errorf("secretbox: unmarshal bound envelope: %w", err)
	}
	if b.Version != boundEnvelopeVersion {
		return nil, Binding{}, fmt.Errorf("secretbox: bound envelope version %d is not supported", b.Version)
	}
	if err := b.Binding.validate(); err != nil {
		return nil, Binding{}, err
	}
	if err := b.Values.Validate(); err != nil {
		return nil, Binding{}, err
	}
	if b.Values == nil {
		b.Values = Envelope{}
	}
	return b.Values, b.Binding, nil
}
