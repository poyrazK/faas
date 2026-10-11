package copycontents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const sealedNamespace = "gregale-postgres-copy-stored-contents-v1"

type Sealed struct {
	Scope                                                      copyinventory.Scope
	SourceDatabaseOID                                          uint32
	InventoryFingerprint, Fingerprint, KeyID, CiphertextSHA256 string
	Ciphertext                                                 []byte `json:"-"`
}

func (Sealed) String() string     { return "sealed private PostgreSQL stored contents" }
func (s Sealed) GoString() string { return s.String() }
func (s Sealed) ValidateMetadata() error {
	if len(s.Ciphertext) > api.PostgresCopyCiphertextMaxBytes {
		return pgerrors.ErrQuotaExceeded
	}
	key, e := age.ParseX25519Recipient(s.KeyID)
	if e != nil || key.String() != s.KeyID || s.Scope.Validate() != nil || s.SourceDatabaseOID == 0 || !validDigest(s.InventoryFingerprint) || !validDigest(s.Fingerprint) || !validDigest(s.CiphertextSHA256) || len(s.Ciphertext) == 0 {
		return pgerrors.ErrInvalid
	}
	hash := sha256.Sum256(s.Ciphertext)
	if hex.EncodeToString(hash[:]) != s.CiphertextSHA256 {
		return pgerrors.ErrConflict
	}
	return nil
}
func Seal(recipient *age.X25519Recipient, m Manifest) (Sealed, error) {
	if recipient == nil {
		return Sealed{}, pgerrors.ErrInvalid
	}
	if e := m.validate(); e != nil {
		return Sealed{}, e
	}
	raw, e := json.Marshal(m.body)
	if e != nil {
		return Sealed{}, pgerrors.ErrUnavailable
	}
	cipher, e := secretbox.SealBytes(recipient, sealedNamespace, raw, api.PostgresCopyEnvelopeMaxBytes)
	if e != nil {
		return Sealed{}, pgerrors.ErrUnavailable
	}
	hash := sha256.Sum256(cipher)
	s := Sealed{m.body.Source.Scope, m.body.Source.Database.OID, m.body.Source.InventoryFingerprint, m.fingerprint, recipient.String(), hex.EncodeToString(hash[:]), cipher}
	return s, s.ValidateMetadata()
}

// Open uses the retained recipient, original complete source requirement and
// ciphertext. It neither recaptures today's source nor changes its digest key.
func Open(identities []*age.X25519Identity, expected copyinventory.DatabaseExport, s Sealed) (Manifest, error) {
	if e := s.ValidateMetadata(); e != nil {
		return Manifest{}, e
	}
	if !validSource(expected) || !s.Scope.Equal(expected.Scope) || s.SourceDatabaseOID != expected.Database.OID || s.InventoryFingerprint != expected.InventoryFingerprint {
		return Manifest{}, pgerrors.ErrConflict
	}
	matching := []*age.X25519Identity{}
	for _, id := range identities {
		if id != nil && id.Recipient().String() == s.KeyID {
			matching = append(matching, id)
		}
	}
	if len(matching) == 0 {
		return Manifest{}, pgerrors.ErrUnavailable
	}
	ns, raw, e := secretbox.OpenBytesMulti(matching, s.Ciphertext)
	if e != nil || ns != sealedNamespace {
		return Manifest{}, pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyInventoryMaxBytes {
		return Manifest{}, pgerrors.ErrQuotaExceeded
	}
	m := Manifest{fingerprint: s.Fingerprint}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m.body) != nil || d.Decode(new(any)) != io.EOF || !reflect.DeepEqual(copyinventory.DatabaseExport(m.body.Source), expected) {
		return Manifest{}, pgerrors.ErrConflict
	}
	if e = m.validate(); e != nil {
		return Manifest{}, e
	}
	return m, nil
}
