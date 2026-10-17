package internalmail

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// Fingerprint is a resource- and namespace-bound HMAC, not a public body hash.
// Keeping this verifier after pruning does not expose low-entropy mail content
// to offline guessing by a reader of the database. Preserve the dedicated age
// identity for replay validation, including after all payloads have expired.
func (c AgeCodec) Fingerprint(resource string, data []byte) ([]byte, error) {
	if c.Identity == nil || !validNamespace(c.Namespace) {
		return nil, errors.New("internal mail: invalid fingerprint identity")
	}
	mac := hmac.New(sha256.New, []byte(c.Identity.String()))
	_, _ = mac.Write([]byte("internal-mail-fingerprint:v1\x00" + c.Namespace + "\x00" + resource + "\x00"))
	_, _ = mac.Write(data)
	return mac.Sum(nil), nil
}

// AgeCodec binds ciphertext to both the service namespace and the resource ID,
// so copying a DKIM key or message ciphertext to another row fails closed.
type AgeCodec struct {
	Identity  *age.X25519Identity
	Namespace string
}

func (c AgeCodec) Seal(resource string, data []byte) ([]byte, error) {
	if c.Identity == nil || !validNamespace(c.Namespace) {
		return nil, errors.New("internal mail: invalid encryption identity")
	}
	return secretbox.SealBytes(c.Identity.Recipient(), "internal-mail:"+c.Namespace+":"+resource, data, api.InternalMailMaxPayloadBytes)
}

func (c AgeCodec) Open(resource string, data []byte) ([]byte, error) {
	ns, plain, err := secretbox.OpenBytes(c.Identity, data)
	if err != nil {
		return nil, err
	}
	if ns != "internal-mail:"+c.Namespace+":"+resource {
		clear(plain)
		return nil, errors.New("internal mail: ciphertext resource mismatch")
	}
	return plain, nil
}
