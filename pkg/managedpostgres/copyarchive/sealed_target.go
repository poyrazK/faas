package copyarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const sealedTargetNamespace = "gregale-postgres-copy-target-pins-v1"

// SealedTarget retains SQL names/OIDs and endpoint pins only inside encryption.
// The outer owner/project pins can be checked against durable preparation without
// decrypting. This descriptor is private metadata, never dispatch/readiness proof.
type SealedTarget struct {
	Scope                                copyinventory.Scope
	OwnerID, ProviderResourceID          string
	ProviderCreatedAt                    time.Time
	Fingerprint, KeyID, CiphertextSHA256 string
	Ciphertext                           []byte `json:"-"`
}

func (SealedTarget) String() string     { return "sealed private PostgreSQL target pins" }
func (s SealedTarget) GoString() string { return s.String() }

func (s SealedTarget) ValidateMetadata() error {
	if s.Scope.Validate() != nil || !opaquePin(s.ProviderResourceID) {
		return pgerrors.ErrInvalid
	}
	id, err := uuid.Parse(s.OwnerID)
	if err != nil || id == uuid.Nil || id.String() != s.OwnerID {
		return pgerrors.ErrInvalid
	}
	for _, source := range []string{s.Scope.OperationID, s.Scope.AccountID, s.Scope.ProjectID, s.Scope.SourceDatabaseID, s.Scope.CaptureDatabaseID} {
		if s.OwnerID == source {
			return pgerrors.ErrInvalid
		}
	}
	for _, source := range []string{s.Scope.SourceProviderResourceID, s.Scope.SourceDataResourceID, s.Scope.CaptureProviderResourceID, s.Scope.ProviderSnapshotID} {
		if s.ProviderResourceID == source {
			return pgerrors.ErrInvalid
		}
	}
	if s.ProviderCreatedAt.IsZero() || s.ProviderCreatedAt.Year() < 1 || s.ProviderCreatedAt.Year() > 9999 || s.ProviderCreatedAt.Nanosecond()%1000 != 0 ||
		s.ProviderCreatedAt.Before(s.Scope.CaptureCreatedAt) || s.ProviderCreatedAt.After(time.Now()) {
		return pgerrors.ErrInvalid
	}
	if len(s.Ciphertext) > api.PostgresCopyCiphertextMaxBytes {
		return pgerrors.ErrQuotaExceeded
	}
	recipient, err := age.ParseX25519Recipient(s.KeyID)
	if err != nil || recipient.String() != s.KeyID || len(s.Ciphertext) == 0 || !targetDigest(s.Fingerprint) || !targetDigest(s.CiphertextSHA256) {
		return pgerrors.ErrInvalid
	}
	hash := sha256.Sum256(s.Ciphertext)
	if hex.EncodeToString(hash[:]) != s.CiphertextSHA256 {
		return pgerrors.ErrConflict
	}
	return nil
}

func targetDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil && hex.EncodeToString(raw) == value
}

type privateTargetPins RestoreTarget
type privateTargetEnvelope struct {
	Version int               `json:"version"`
	Target  privateTargetPins `json:"target"`
}

func SealTarget(recipient *age.X25519Recipient, target RestoreTarget) (SealedTarget, error) {
	if recipient == nil {
		return SealedTarget{}, pgerrors.ErrInvalid
	}
	fingerprint, err := target.Fingerprint()
	if err != nil {
		return SealedTarget{}, err
	}
	raw, err := json.Marshal(privateTargetEnvelope{Version: 1, Target: privateTargetPins(target)})
	if err != nil {
		return SealedTarget{}, pgerrors.ErrInvalid
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return SealedTarget{}, pgerrors.ErrQuotaExceeded
	}
	ciphertext, err := secretbox.SealBytes(recipient, sealedTargetNamespace, raw, api.PostgresCopyEnvelopeMaxBytes)
	if err != nil {
		return SealedTarget{}, pgerrors.ErrUnavailable
	}
	hash := sha256.Sum256(ciphertext)
	s := SealedTarget{Scope: target.Scope, OwnerID: target.OwnerID, ProviderResourceID: target.ProviderResourceID, ProviderCreatedAt: target.ProviderCreatedAt.UTC(),
		Fingerprint: fingerprint, KeyID: recipient.String(), CiphertextSHA256: hex.EncodeToString(hash[:]), Ciphertext: ciphertext}
	return s, s.ValidateMetadata()
}

// Open only committed private pins in their original scope. Current/previous
// identities support key rotation without re-observing or reselecting SQL IDs.
func OpenTarget(identities []*age.X25519Identity, expected copyinventory.Scope, sealed SealedTarget) (RestoreTarget, error) {
	if expected.Validate() != nil {
		return RestoreTarget{}, pgerrors.ErrInvalid
	}
	if err := sealed.ValidateMetadata(); err != nil {
		return RestoreTarget{}, err
	}
	if !expected.Equal(sealed.Scope) {
		return RestoreTarget{}, pgerrors.ErrConflict
	}
	var matching []*age.X25519Identity
	for _, id := range identities {
		if id != nil && id.Recipient().String() == sealed.KeyID {
			matching = append(matching, id)
		}
	}
	if len(matching) == 0 {
		return RestoreTarget{}, pgerrors.ErrUnavailable
	}
	namespace, raw, err := secretbox.OpenBytesMulti(matching, sealed.Ciphertext)
	if err != nil || namespace != sealedTargetNamespace {
		return RestoreTarget{}, pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return RestoreTarget{}, pgerrors.ErrQuotaExceeded
	}
	var envelope privateTargetEnvelope
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&envelope) != nil || d.Decode(new(any)) != io.EOF || envelope.Version != 1 {
		return RestoreTarget{}, pgerrors.ErrConflict
	}
	target := RestoreTarget(envelope.Target)
	fingerprint, err := target.Fingerprint()
	if err != nil || fingerprint != sealed.Fingerprint || !target.Scope.Equal(expected) || target.OwnerID != sealed.OwnerID || target.ProviderResourceID != sealed.ProviderResourceID || !target.ProviderCreatedAt.Equal(sealed.ProviderCreatedAt) {
		return RestoreTarget{}, pgerrors.ErrConflict
	}
	return target, nil
}
