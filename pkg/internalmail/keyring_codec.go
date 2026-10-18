package internalmail

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const encryptionPrefix = "GMAIL2:"
const fingerprintPrefix = "GMFP2:"

// KeyringCodec keeps the configured read keys immutable. Only the durable
// active selection changes. PostgreSQL fences stale writers at commit; an
// in-flight seal may safely fail after a concurrent activation.
type KeyringCodec struct {
	namespace string
	legacyID  string
	keys      map[string]*age.X25519Identity
	mu        sync.RWMutex
	activeID  string
	versioned bool
	revision  int64
}

func EncryptionKeyID(identity *age.X25519Identity) string {
	if identity == nil {
		return ""
	}
	sum := sha256.Sum256([]byte("gregale-mail-encryption-key:v1\x00" + identity.Recipient().String()))
	return hex.EncodeToString(sum[:])
}

func validEncryptionKeyID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func NewKeyringCodec(namespace, legacyID string, identities []*age.X25519Identity) (*KeyringCodec, error) {
	if !validNamespace(namespace) || !validEncryptionKeyID(legacyID) || len(identities) == 0 || len(identities) > api.InternalMailMaxEncryptionKeys {
		return nil, ErrInvalid
	}
	c := &KeyringCodec{namespace: namespace, legacyID: legacyID, keys: map[string]*age.X25519Identity{}, activeID: legacyID}
	for _, identity := range identities {
		id := EncryptionKeyID(identity)
		if id == "" || c.keys[id] != nil {
			return nil, ErrInvalid
		}
		c.keys[id] = identity
	}
	return c, nil
}

func (c *KeyringCodec) configuredIDs() []string {
	ids := make([]string, 0, len(c.keys))
	for id := range c.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *KeyringCodec) selection() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeID, c.versioned
}

func (c *KeyringCodec) selectKey(id string, revision int64) error {
	if c.keys[id] == nil {
		return ErrEncryptionKeyUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if revision < c.revision {
		return nil
	}
	if revision == c.revision && id != c.activeID {
		return ErrEncryptionRevision
	}
	c.activeID, c.versioned, c.revision = id, revision > 0, revision
	return nil
}

func envelopeKeyID(data []byte) (string, error) {
	if !bytes.HasPrefix(data, []byte(encryptionPrefix)) {
		return "legacy", nil
	}
	return versionedKeyID(data, encryptionPrefix, false)
}

func fingerprintKeyID(data []byte) (string, error) {
	if len(data) == sha256.Size {
		return "legacy", nil
	}
	return versionedKeyID(data, fingerprintPrefix, true)
}

func versionedKeyID(data []byte, prefix string, fingerprint bool) (string, error) {
	offset := len(prefix) + sha256.Size*2
	if len(data) <= offset || !bytes.HasPrefix(data, []byte(prefix)) || data[offset] != ':' || fingerprint && len(data) != offset+1+sha256.Size {
		return "", ErrInvalid
	}
	id := string(data[len(prefix):offset])
	if !validEncryptionKeyID(id) {
		return "", ErrInvalid
	}
	return id, nil
}

func (c *KeyringCodec) Seal(resource string, plain []byte) ([]byte, error) {
	id, versioned := c.selection()
	identity := c.keys[id]
	if identity == nil {
		return nil, ErrEncryptionKeyUnavailable
	}
	if !versioned {
		return (AgeCodec{Identity: identity, Namespace: c.namespace}).Seal(resource, plain)
	}
	header := encryptionPrefix + id + ":"
	sealed, err := secretbox.SealBytes(identity.Recipient(), "internal-mail:"+c.namespace+":"+header+resource, plain, api.InternalMailMaxPayloadBytes)
	if err != nil {
		return nil, errors.New("internal mail: payload encryption failed")
	}
	return append([]byte(header), sealed...), nil
}

func (c *KeyringCodec) Open(resource string, data []byte) ([]byte, error) {
	id, err := envelopeKeyID(data)
	if err != nil {
		return nil, err
	}
	if id == "legacy" {
		identity := c.keys[c.legacyID]
		if identity == nil {
			return nil, ErrEncryptionKeyUnavailable
		}
		return (AgeCodec{Identity: identity, Namespace: c.namespace}).Open(resource, data)
	}
	identity := c.keys[id]
	if identity == nil {
		return nil, ErrEncryptionKeyUnavailable
	}
	header := encryptionPrefix + id + ":"
	ns, plain, err := secretbox.OpenBytes(identity, data[len(header):])
	if err != nil {
		return nil, errors.New("internal mail: payload decryption failed")
	}
	if ns != "internal-mail:"+c.namespace+":"+header+resource {
		clear(plain)
		return nil, errors.New("internal mail: ciphertext resource mismatch")
	}
	return plain, nil
}

func (c *KeyringCodec) Fingerprint(resource string, plain []byte) ([]byte, error) {
	id, versioned := c.selection()
	if !versioned {
		return (AgeCodec{Identity: c.keys[id], Namespace: c.namespace}).Fingerprint(resource, plain)
	}
	mac, err := c.fingerprintV2(id, resource, plain)
	if err != nil {
		return nil, err
	}
	return append([]byte(fingerprintPrefix+id+":"), mac...), nil
}

func (c *KeyringCodec) fingerprintV2(id, resource string, plain []byte) ([]byte, error) {
	identity := c.keys[id]
	if identity == nil {
		return nil, ErrEncryptionKeyUnavailable
	}
	mac := hmac.New(sha256.New, []byte(identity.String()))
	_, _ = mac.Write([]byte("internal-mail-fingerprint:v2\x00" + c.namespace + "\x00" + id + "\x00" + resource + "\x00"))
	_, _ = mac.Write(plain)
	return mac.Sum(nil), nil
}

func (c *KeyringCodec) VerifyFingerprint(resource string, plain, fingerprint []byte) error {
	id, err := fingerprintKeyID(fingerprint)
	if err != nil {
		return err
	}
	if id == "legacy" {
		if c.keys[c.legacyID] == nil {
			return ErrEncryptionKeyUnavailable
		}
		return (AgeCodec{Identity: c.keys[c.legacyID], Namespace: c.namespace}).VerifyFingerprint(resource, plain, fingerprint)
	}
	expected, err := c.fingerprintV2(id, resource, plain)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected, fingerprint[len(fingerprintPrefix)+sha256.Size*2+1:]) {
		return ErrConflict
	}
	return nil
}

func verifyFingerprint(codec Codec, resource string, plain, fingerprint []byte) error {
	if verifier, ok := codec.(interface {
		VerifyFingerprint(string, []byte, []byte) error
	}); ok {
		return verifier.VerifyFingerprint(resource, plain, fingerprint)
	}
	expected, err := codec.Fingerprint(resource, plain)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected, fingerprint) {
		return ErrConflict
	}
	return nil
}

func (c AgeCodec) VerifyFingerprint(resource string, plain, fingerprint []byte) error {
	if len(fingerprint) != sha256.Size {
		return ErrEncryptionKeyUnavailable
	}
	expected, err := c.Fingerprint(resource, plain)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected, fingerprint) {
		return ErrConflict
	}
	return nil
}

func encryptionRoute(path string) bool {
	return path == EncryptionPath || strings.HasPrefix(path, EncryptionPath+"/")
}
