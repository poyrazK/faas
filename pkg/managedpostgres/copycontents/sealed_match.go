package copycontents

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const matchNamespace = "gregale-postgres-copy-contents-match-v1"

// A retained match is a data comparison, not closure or readiness. Its MAC uses
// the original manifest's private comparison key. Knowing an age recipient or
// replacing metadata cannot invent a successful independent comparison.
type SealedMatch struct {
	Scope                                  copyinventory.Scope
	SourceDatabaseOID, TargetDatabaseOID   uint32
	OwnerID, ImportID                      string
	OpenedAt                               time.Time
	ManifestFingerprint, TargetFingerprint string
	Fingerprint, KeyID, CiphertextSHA256   string
	Ciphertext                             []byte
}

func (SealedMatch) String() string               { return "sealed private PostgreSQL contents match" }
func (s SealedMatch) GoString() string           { return s.String() }
func (SealedMatch) MarshalJSON() ([]byte, error) { return json.Marshal(struct{}{}) }

func (s SealedMatch) ValidateMetadata() error {
	key, err := age.ParseX25519Recipient(s.KeyID)
	owner, ownerErr := uuid.Parse(s.OwnerID)
	imported, importErr := uuid.Parse(s.ImportID)
	if s.Scope.Validate() != nil || s.SourceDatabaseOID == 0 || s.TargetDatabaseOID == 0 || ownerErr != nil || importErr != nil ||
		owner == uuid.Nil || imported == uuid.Nil || owner == imported || owner.String() != s.OwnerID || imported.String() != s.ImportID ||
		!validDigest(s.ManifestFingerprint) || !validDigest(s.TargetFingerprint) || !validDigest(s.Fingerprint) || !validDigest(s.CiphertextSHA256) ||
		err != nil || key.String() != s.KeyID || !matchTime(s.OpenedAt) || s.OpenedAt.Before(s.Scope.CaptureCreatedAt) || len(s.Ciphertext) == 0 {
		return pgerrors.ErrInvalid
	}
	for _, id := range []string{s.Scope.OperationID, s.Scope.AccountID, s.Scope.ProjectID, s.Scope.SourceDatabaseID, s.Scope.CaptureDatabaseID} {
		if s.OwnerID == id || s.ImportID == id {
			return pgerrors.ErrInvalid
		}
	}
	if len(s.Ciphertext) > api.PostgresCopyVerificationCiphertextMaxBytes {
		return pgerrors.ErrQuotaExceeded
	}
	h := sha256.Sum256(s.Ciphertext)
	if hex.EncodeToString(h[:]) != s.CiphertextSHA256 {
		return pgerrors.ErrConflict
	}
	return nil
}

type matchEnvelope struct {
	Version                                int
	Scope                                  copyinventory.Scope
	SourceOID, TargetOID                   uint32
	OwnerID, ImportID                      string
	OpenedAt                               time.Time
	ManifestFingerprint, TargetFingerprint string
}

func matchTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0
}

// SealMatch must run after CompareTarget and its borrowed transaction/provider
// checks, while the original verification window remains open. Durable ownership
// supplies both owners and the first SQL opening time; closure remains separate.
func SealMatch(recipient *age.X25519Recipient, manifest Manifest, target copyarchive.RestoreTarget, owner, imported uuid.UUID, openedAt time.Time, match Match) (SealedMatch, error) {
	if recipient == nil || !match.Matches(manifest, target) || owner == uuid.Nil || imported == uuid.Nil || owner == imported ||
		owner.String() == target.OwnerID || imported.String() == target.OwnerID || !matchTime(openedAt) || openedAt.Before(target.ProviderCreatedAt) {
		return SealedMatch{}, pgerrors.ErrInvalid
	}
	fp, _ := target.Fingerprint()
	e := matchEnvelope{1, target.Scope, match.sourceOID, match.targetOID, owner.String(), imported.String(), openedAt.UTC(), manifest.Fingerprint(), fp}
	raw, err := json.Marshal(e)
	if err != nil || len(raw) > api.PostgresCopyVerificationEnvelopeMaxBytes {
		return SealedMatch{}, pgerrors.ErrQuotaExceeded
	}
	cipher, err := secretbox.SealBytes(recipient, matchNamespace, raw, api.PostgresCopyVerificationEnvelopeMaxBytes)
	if err != nil {
		return SealedMatch{}, pgerrors.ErrUnavailable
	}
	h := sha256.Sum256(cipher)
	s := SealedMatch{Scope: target.Scope, SourceDatabaseOID: match.sourceOID, TargetDatabaseOID: match.targetOID,
		OwnerID: owner.String(), ImportID: imported.String(), OpenedAt: openedAt.UTC(), ManifestFingerprint: manifest.Fingerprint(), TargetFingerprint: fp,
		Fingerprint: hex.EncodeToString(keyedHashSum(manifest.body.Key, matchNamespace, raw)), KeyID: recipient.String(), CiphertextSHA256: hex.EncodeToString(h[:]), Ciphertext: cipher}
	return s, s.ValidateMetadata()
}

// Recover only the original bound comparison. This never re-reads source/target
// data and grants no verification-window access or publication authority.
func OpenMatch(ids []*age.X25519Identity, manifest Manifest, target copyarchive.RestoreTarget, owner, imported uuid.UUID, s SealedMatch) (RetainedMatch, error) {
	if err := s.ValidateMetadata(); err != nil {
		return RetainedMatch{}, err
	}
	fp, err := target.Fingerprint()
	if err != nil || manifest.validate() != nil || owner == uuid.Nil || imported == uuid.Nil || s.OwnerID != owner.String() || s.ImportID != imported.String() ||
		!s.Scope.Equal(target.Scope) || !target.Scope.Equal(manifest.body.Source.Scope) || s.SourceDatabaseOID != manifest.body.Source.Database.OID ||
		s.TargetDatabaseOID != target.DatabaseOID || s.ManifestFingerprint != manifest.Fingerprint() || s.TargetFingerprint != fp ||
		s.OpenedAt.Before(target.ProviderCreatedAt) || s.OwnerID == target.OwnerID || s.ImportID == target.OwnerID {
		return RetainedMatch{}, pgerrors.ErrConflict
	}
	var matching []*age.X25519Identity
	for _, id := range ids {
		if id != nil && id.Recipient().String() == s.KeyID {
			matching = append(matching, id)
		}
	}
	if len(matching) == 0 {
		return RetainedMatch{}, pgerrors.ErrUnavailable
	}
	ns, raw, err := secretbox.OpenBytesMulti(matching, s.Ciphertext)
	if err != nil || ns != matchNamespace {
		return RetainedMatch{}, pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyVerificationEnvelopeMaxBytes {
		return RetainedMatch{}, pgerrors.ErrQuotaExceeded
	}
	var e matchEnvelope
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF || e.Version != 1 || !e.Scope.Equal(s.Scope) || e.SourceOID != s.SourceDatabaseOID ||
		e.TargetOID != s.TargetDatabaseOID || e.OwnerID != s.OwnerID || e.ImportID != s.ImportID || !e.OpenedAt.Equal(s.OpenedAt) ||
		e.ManifestFingerprint != s.ManifestFingerprint || e.TargetFingerprint != s.TargetFingerprint || !hmac.Equal(keyedHashSum(manifest.body.Key, matchNamespace, raw), mustDigest(s.Fingerprint)) {
		return RetainedMatch{}, pgerrors.ErrConflict
	}
	return RetainedMatch{Match{manifest.Fingerprint(), fp, s.SourceDatabaseOID, s.TargetDatabaseOID}, s.OwnerID, s.ImportID, s.KeyID, s.Fingerprint, s.CiphertextSHA256, s.OpenedAt}, nil
}

// Only successful authenticated open constructs this capability. Final durable
// publication requires the exact first ciphertext, not merely an earlier match
// whose privately retained proof may have been damaged or substituted.
type RetainedMatch struct {
	comparison                                              Match
	ownerID, importID, keyID, fingerprint, ciphertextSHA256 string
	openedAt                                                time.Time
}

func (RetainedMatch) String() string               { return "private retained PostgreSQL contents match" }
func (m RetainedMatch) GoString() string           { return m.String() }
func (RetainedMatch) MarshalJSON() ([]byte, error) { return json.Marshal(struct{}{}) }
func (m RetainedMatch) Matches(manifest Manifest, target copyarchive.RestoreTarget, sealed SealedMatch) bool {
	return sealed.ValidateMetadata() == nil && m.comparison.Matches(manifest, target) && sealed.Scope.Equal(target.Scope) &&
		sealed.ManifestFingerprint == manifest.Fingerprint() && sealed.TargetFingerprint == m.comparison.targetFingerprint &&
		sealed.SourceDatabaseOID == m.comparison.sourceOID && sealed.TargetDatabaseOID == m.comparison.targetOID &&
		m.ownerID == sealed.OwnerID && m.importID == sealed.ImportID && m.keyID == sealed.KeyID && m.fingerprint == sealed.Fingerprint &&
		m.ciphertextSHA256 == sealed.CiphertextSHA256 && m.openedAt.Equal(sealed.OpenedAt)
}
