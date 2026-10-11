package copydatabases

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const namespace = "gregale-postgres-copy-database-plan-v1"

type Sealed copyroles.Sealed

func (Sealed) String() string            { return "sealed private PostgreSQL database plan" }
func (s Sealed) GoString() string        { return s.String() }
func (s Sealed) ValidateMetadata() error { return copyroles.Sealed(s).ValidateMetadata() }
func Seal(recipient *age.X25519Recipient, p Plan) (Sealed, error) {
	if recipient == nil {
		return Sealed{}, pgerrors.ErrInvalid
	}
	raw, err := p.PrivatePayloadForSealing()
	if err != nil {
		return Sealed{}, err
	}
	cipher, err := secretbox.SealBytes(recipient, namespace, raw, api.PostgresCopyEnvelopeMaxBytes)
	if err != nil {
		return Sealed{}, pgerrors.ErrUnavailable
	}
	h := sha256.Sum256(cipher)
	s := Sealed{Scope: p.body.Target.Scope, InventoryFingerprint: p.body.InventoryFingerprint, TargetFingerprint: p.body.TargetFingerprint, KeyID: recipient.String(), CiphertextSHA256: hex.EncodeToString(h[:]), Ciphertext: cipher}
	return s, s.ValidateMetadata()
}

// Original source and bootstrap pins recover the retained database/role OIDs and
// pre-write target catalogue without target SQL, source rereads or a new recipient.
func Open(ids []*age.X25519Identity, source copyinventory.ExportPlan, target copyarchive.RestoreTarget, s Sealed) (Plan, error) {
	if err := s.ValidateMetadata(); err != nil {
		return Plan{}, err
	}
	fp, err := target.Fingerprint()
	if err != nil || fp != s.TargetFingerprint || !target.Scope.Equal(s.Scope) {
		return Plan{}, pgerrors.ErrConflict
	}
	var matching []*age.X25519Identity
	for _, id := range ids {
		if id != nil && id.Recipient().String() == s.KeyID {
			matching = append(matching, id)
		}
	}
	if len(matching) == 0 {
		return Plan{}, pgerrors.ErrUnavailable
	}
	ns, raw, err := secretbox.OpenBytesMulti(matching, s.Ciphertext)
	if err != nil || ns != namespace {
		return Plan{}, pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return Plan{}, pgerrors.ErrQuotaExceeded
	}
	var p Plan
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p.body) != nil || d.Decode(new(any)) != io.EOF || p.validate(source) != nil || copyarchive.RestoreTarget(p.body.Target) != target || p.body.InventoryFingerprint != s.InventoryFingerprint || p.body.TargetFingerprint != s.TargetFingerprint {
		return Plan{}, pgerrors.ErrConflict
	}
	return p, nil
}
