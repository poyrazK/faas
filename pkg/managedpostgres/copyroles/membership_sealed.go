package copyroles

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

const membershipNamespace = "gregale-postgres-copy-membership-plan-v1"

// A distinct ciphertext type prevents replacing a role seed plan with its later
// membership plan. Both bind the original inventory and target fingerprints.
type SealedMemberships Sealed

func (SealedMemberships) String() string            { return "sealed private PostgreSQL membership plan" }
func (s SealedMemberships) GoString() string        { return s.String() }
func (s SealedMemberships) ValidateMetadata() error { return Sealed(s).ValidateMetadata() }

func SealMemberships(recipient *age.X25519Recipient, plan MembershipPlan) (SealedMemberships, error) {
	if recipient == nil {
		return SealedMemberships{}, pgerrors.ErrInvalid
	}
	raw, err := plan.PrivatePayloadForSealing()
	if err != nil {
		return SealedMemberships{}, err
	}
	cipher, err := secretbox.SealBytes(recipient, membershipNamespace, raw, api.PostgresCopyEnvelopeMaxBytes)
	if err != nil {
		return SealedMemberships{}, pgerrors.ErrUnavailable
	}
	hash := sha256.Sum256(cipher)
	s := SealedMemberships{Scope: plan.body.SeedPlan.Target.Scope, InventoryFingerprint: plan.body.SeedPlan.InventoryFingerprint, TargetFingerprint: plan.body.SeedPlan.TargetFingerprint,
		KeyID: recipient.String(), CiphertextSHA256: hex.EncodeToString(hash[:]), Ciphertext: cipher}
	return s, s.ValidateMetadata()
}

// Recover only with original source metadata, seed OIDs/time and recipient. A
// changed membership catalogue or different role seed can never rebase this plan.
func OpenMemberships(identities []*age.X25519Identity, source copyinventory.ExportPlan, seed Receipt, s SealedMemberships) (MembershipPlan, error) {
	if err := s.ValidateMetadata(); err != nil {
		return MembershipPlan{}, err
	}
	if seed.validate() != nil || !s.Scope.Equal(seed.plan.body.Target.Scope) || s.InventoryFingerprint != seed.plan.body.InventoryFingerprint || s.TargetFingerprint != seed.plan.body.TargetFingerprint {
		return MembershipPlan{}, pgerrors.ErrConflict
	}
	var matching []*age.X25519Identity
	for _, id := range identities {
		if id != nil && id.Recipient().String() == s.KeyID {
			matching = append(matching, id)
		}
	}
	if len(matching) == 0 {
		return MembershipPlan{}, pgerrors.ErrUnavailable
	}
	namespace, raw, err := secretbox.OpenBytesMulti(matching, s.Ciphertext)
	if err != nil || namespace != membershipNamespace {
		return MembershipPlan{}, pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return MembershipPlan{}, pgerrors.ErrQuotaExceeded
	}
	var plan MembershipPlan
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&plan.body) != nil || d.Decode(new(any)) != io.EOF || plan.validate() != nil ||
		!reflect.DeepEqual(plan.body.SeedPlan, seed.plan.body) || !reflect.DeepEqual(plan.body.SeedReceipt, seed.body) {
		return MembershipPlan{}, pgerrors.ErrConflict
	}
	requirements, err := source.RequirementsForWorker()
	if err != nil {
		return MembershipPlan{}, err
	}
	src, err := source.MembershipCatalogueForWorker()
	if err != nil {
		return MembershipPlan{}, err
	}
	sortMemberships(src.Memberships)
	if len(requirements) == 0 || !requirements[0].Scope.Equal(s.Scope) || !membershipSourceMatchesSeed(src, seed) || !reflect.DeepEqual(src.Memberships, plan.body.SourceMemberships) {
		return MembershipPlan{}, pgerrors.ErrConflict
	}
	return plan, nil
}
