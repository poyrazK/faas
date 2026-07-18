// seal.go — Seal / Open round-trip for customer secret envelopes.
//
// The on-disk ciphertext is age's standard format (X25519 stanza + ChaCha20-
// Poly1305 body). The plaintext is a canonical-JSON encoding of Envelope so
// Open validates shape before returning to callers.
//
// Two callers exist:
//   - apid: Seal(env) → ciphertext stored in app_secrets.ciphertext.
//   - vmmd: Open(ciphertext) → env (decoded Envelope), loopback-mounted to
//     drive1 as /etc/faas/secrets.env.
//
// guest-init does NOT call secretbox.Open. It reads the cleartext
// /etc/faas/secrets.env (decoded by vmmd at provision time). The seal
// boundary is apid → vmmd, not vmmd → guest.
package secretbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
)

// Envelope is the plaintext shape sealed at rest. JSON-marshalled. Map
// iteration order is non-deterministic; canonical encoding comes from
// json.Marshal which sorts map keys.
type Envelope map[string]string

// Validate checks the envelope shape: every key must match the env-var
// pattern enforced by the app_secrets.key CHECK constraint. Values are
// accepted as-is — byte-length enforcement happens upstream in apid against
// Limits.SecretValueMaxBytes so over-cap values never reach the seal path.
func (e Envelope) Validate() error {
	keyRe := regexp.MustCompile(api.SecretKeyPattern)
	for k := range e {
		if len(k) == 0 || len(k) > api.MaxSecretKeyLen {
			return fmt.Errorf("secretbox: key length %d out of range (0,%d]", len(k), api.MaxSecretKeyLen)
		}
		if !keyRe.MatchString(k) {
			return fmt.Errorf("secretbox: key %q does not match %s", k, api.SecretKeyPattern)
		}
	}
	return nil
}

// Seal encrypts env under recipient using age X25519 + ChaCha20-Poly1305.
// The returned blob is the age file format (ASCII-armoured stanza header
// followed by binary body) suitable for bytea storage in PG.
func Seal(recipient *age.X25519Recipient, env Envelope) ([]byte, error) {
	if recipient == nil {
		return nil, errors.New("secretbox: nil recipient")
	}
	if err := env.Validate(); err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("secretbox: marshal envelope: %w", err)
	}
	var out bytes.Buffer
	w, err := age.Encrypt(&out, recipient)
	if err != nil {
		return nil, fmt.Errorf("secretbox: open age writer: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("secretbox: write plaintext: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("secretbox: close age writer: %w", err)
	}
	return out.Bytes(), nil
}

// Open decrypts blob under identity and returns the decoded Envelope.
// Returns an error if the blob is tampered, the identity doesn't match,
// or the plaintext isn't a valid Envelope.
func Open(identity *age.X25519Identity, blob []byte) (Envelope, error) {
	if identity == nil {
		return nil, errors.New("secretbox: nil identity")
	}
	if len(blob) == 0 {
		return nil, errors.New("secretbox: empty blob")
	}
	r, err := age.Decrypt(bytes.NewReader(blob), identity)
	if err != nil {
		return nil, fmt.Errorf("secretbox: open age reader: %w", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("secretbox: read plaintext: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("secretbox: unmarshal envelope: %w", err)
	}
	if err := env.Validate(); err != nil {
		return nil, err
	}
	return env, nil
}

// SealOne seals a single (key, value) pair. Convenience wrapper for apid's
// PUT handler: it builds the one-entry Envelope, validates SecretValueMaxBytes
// against len(value), and returns the ciphertext blob.
//
// The byte cap is checked HERE (not by the caller) so the seal path is the
// single trust boundary for "no over-cap ciphertext ever lands in PG".
func SealOne(recipient *age.X25519Recipient, key, value string, maxValueBytes int) ([]byte, error) {
	if maxValueBytes > 0 && len(value) > maxValueBytes {
		return nil, api.ErrSecretValueTooLarge(api.Limits{SecretValueMaxBytes: maxValueBytes}, len(value))
	}
	return Seal(recipient, Envelope{key: value})
}
