package copyroles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"slices"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const sealedNamespace = "gregale-postgres-copy-role-seed-plan-v1"

type Sealed struct {
	Scope                                                            copyinventory.Scope
	InventoryFingerprint, TargetFingerprint, KeyID, CiphertextSHA256 string
	Ciphertext                                                       []byte `json:"-"`
}

func (Sealed) String() string     { return "sealed private PostgreSQL role seed plan" }
func (s Sealed) GoString() string { return s.String() }
func (s Sealed) ValidateMetadata() error {
	if len(s.Ciphertext) > api.PostgresCopyCiphertextMaxBytes {
		return pgerrors.ErrQuotaExceeded
	}
	key, err := age.ParseX25519Recipient(s.KeyID)
	if err != nil || key.String() != s.KeyID || s.Scope.Validate() != nil || !digest(s.InventoryFingerprint) || !digest(s.TargetFingerprint) || !digest(s.CiphertextSHA256) || len(s.Ciphertext) == 0 {
		return pgerrors.ErrInvalid
	}
	hash := sha256.Sum256(s.Ciphertext)
	if hex.EncodeToString(hash[:]) != s.CiphertextSHA256 {
		return pgerrors.ErrConflict
	}
	return nil
}

func Seal(recipient *age.X25519Recipient, p Plan) (Sealed, error) {
	if recipient == nil {
		return Sealed{}, pgerrors.ErrInvalid
	}
	raw, err := p.PrivatePayloadForSealing()
	if err != nil {
		return Sealed{}, err
	}
	cipher, err := secretbox.SealBytes(recipient, sealedNamespace, raw, api.PostgresCopyEnvelopeMaxBytes)
	if err != nil {
		return Sealed{}, pgerrors.ErrUnavailable
	}
	hash := sha256.Sum256(cipher)
	s := Sealed{Scope: p.body.Target.Scope, InventoryFingerprint: p.body.InventoryFingerprint, TargetFingerprint: p.body.TargetFingerprint,
		KeyID: recipient.String(), CiphertextSHA256: hex.EncodeToString(hash[:]), Ciphertext: cipher}
	return s, s.ValidateMetadata()
}

// Open recovers the original plan, including the pre-write target role baseline.
// It checks the original every-database plan and exact committed bootstrap pins;
// neither today's target catalogue nor a new encryption recipient can rebase it.
func Open(identities []*age.X25519Identity, source copyinventory.ExportPlan, target copyarchive.RestoreTarget, s Sealed) (Plan, error) {
	if err := s.ValidateMetadata(); err != nil {
		return Plan{}, err
	}
	fingerprint, err := target.Fingerprint()
	if err != nil {
		return Plan{}, err
	}
	requirements, err := source.RequirementsForWorker()
	if err != nil {
		return Plan{}, err
	}
	if len(requirements) == 0 || !requirements[0].Scope.Equal(target.Scope) || !s.Scope.Equal(target.Scope) || fingerprint != s.TargetFingerprint || requirements[0].InventoryFingerprint != s.InventoryFingerprint {
		return Plan{}, pgerrors.ErrConflict
	}
	var matching []*age.X25519Identity
	for _, id := range identities {
		if id != nil && id.Recipient().String() == s.KeyID {
			matching = append(matching, id)
		}
	}
	if len(matching) == 0 {
		return Plan{}, pgerrors.ErrUnavailable
	}
	namespace, raw, err := secretbox.OpenBytesMulti(matching, s.Ciphertext)
	if err != nil || namespace != sealedNamespace {
		return Plan{}, pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return Plan{}, pgerrors.ErrQuotaExceeded
	}
	var p Plan
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p.body) != nil || d.Decode(new(any)) != io.EOF {
		return Plan{}, pgerrors.ErrConflict
	}
	if err := p.validate(); err != nil {
		return Plan{}, err
	}
	if p.body.TargetFingerprint != s.TargetFingerprint || p.body.InventoryFingerprint != s.InventoryFingerprint {
		return Plan{}, pgerrors.ErrConflict
	}
	src, err := source.RoleCatalogueForWorker()
	if err != nil {
		return Plan{}, err
	}
	slices.SortFunc(src.Roles, func(a, b copyinventory.Role) int { return compareOID(a.OID, b.OID) })
	if len(src.Roles) != len(p.body.Roles) {
		return Plan{}, pgerrors.ErrConflict
	}
	for n, r := range src.Roles {
		if !reflect.DeepEqual(r, p.body.Roles[n].Source) {
			return Plan{}, pgerrors.ErrConflict
		}
	}
	return p, nil
}
