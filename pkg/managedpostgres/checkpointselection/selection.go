// Package checkpointselection retains a private source database selection before
// connection closure. It does not attest complete inventory/writer coverage,
// authorize SQL or choose a common configuration/database/object capture point.
package checkpointselection

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const namespace = "gregale-postgres-checkpoint-selection-v1"

// Scope must be derived from a frozen clone, its owned source recovery hold and
// authenticated ready maintenance receipt. There is deliberately no data point.
type Scope struct {
	OperationID, AccountID, ProjectID, SourceDatabaseID, MaintenanceID string
	SourceVersion, BackendID, BackendFingerprint                       string
	SourceProviderResourceID, SourceDataResourceID                     string
	PostgresMajor                                                      int
	MaintenanceOwnerOID, MaintenanceDatabaseOID                        uint32
}

func (s Scope) Validate() error {
	for _, id := range []string{s.OperationID, s.AccountID, s.ProjectID, s.SourceDatabaseID, s.MaintenanceID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return pgerrors.ErrInvalid
		}
	}
	for _, id := range []string{s.BackendID, s.SourceProviderResourceID, s.SourceDataResourceID} {
		if id == "" || len(id) > 255 || !utf8.ValidString(id) || strings.ContainsRune(id, 0) {
			return pgerrors.ErrInvalid
		}
	}
	if s.OperationID == s.MaintenanceID || s.SourceDatabaseID == s.MaintenanceID || !hexDigest(s.SourceVersion) || !hexDigest(s.BackendFingerprint) ||
		s.PostgresMajor < 16 || s.PostgresMajor > 99 || s.MaintenanceOwnerOID == 0 || s.MaintenanceDatabaseOID == 0 {
		return pgerrors.ErrInvalid
	}
	return nil
}

type payload struct {
	Version       int      `json:"version"`
	Scope         Scope    `json:"scope"`
	DatabaseNames []string `json:"database_names"`
}

type envelope struct {
	Payload     payload  `json:"selection"`
	Key         [32]byte `json:"fingerprint_key"`
	Fingerprint string   `json:"fingerprint"`
}

// Selection has immutable private input. Only an explicit worker accessor
// returns names, with an independent slice. It is not dispatch/closure evidence.
type Selection struct {
	body        payload
	fingerprint string
}

func (Selection) String() string     { return "private PostgreSQL checkpoint selection" }
func (s Selection) GoString() string { return s.String() }
func (s Selection) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Fingerprint string }{s.fingerprint})
}

func (s Selection) RequestForWorker(expected Scope) (managedpostgres.CheckpointConnectionRequest, error) {
	if expected.Validate() != nil {
		return managedpostgres.CheckpointConnectionRequest{}, pgerrors.ErrInvalid
	}
	if !hexDigest(s.fingerprint) || s.body.Scope != expected || !validPayload(s.body) {
		return managedpostgres.CheckpointConnectionRequest{}, pgerrors.ErrConflict
	}
	return managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{
		OwnerToken: expected.OperationID, SourceResourceID: expected.SourceDataResourceID}, DatabaseNames: slices.Clone(s.body.DatabaseNames)}, nil
}

type Sealed struct {
	Scope                                Scope
	Fingerprint, KeyID, CiphertextSHA256 string
	Ciphertext                           []byte `json:"-"`
}

func (Sealed) String() string     { return "sealed private PostgreSQL checkpoint selection" }
func (s Sealed) GoString() string { return s.String() }
func (s Sealed) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Fingerprint, KeyID, CiphertextSHA256 string }{s.Fingerprint, s.KeyID, s.CiphertextSHA256})
}

func (s Sealed) ValidateMetadata() error {
	if s.Scope.Validate() != nil {
		return pgerrors.ErrInvalid
	}
	if len(s.Ciphertext) > api.PostgresCopyCiphertextMaxBytes {
		return pgerrors.ErrQuotaExceeded
	}
	key, err := age.ParseX25519Recipient(s.KeyID)
	if err != nil || key.String() != s.KeyID || len(s.Ciphertext) == 0 || !hexDigest(s.Fingerprint) || !hexDigest(s.CiphertextSHA256) {
		return pgerrors.ErrInvalid
	}
	hash := sha256.Sum256(s.Ciphertext)
	if hex.EncodeToString(hash[:]) != s.CiphertextSHA256 {
		return pgerrors.ErrConflict
	}
	return nil
}

func Seal(recipient *age.X25519Recipient, scope Scope, request managedpostgres.CheckpointConnectionRequest, key [32]byte) (Sealed, error) {
	if recipient == nil || scope.Validate() != nil || request.Validate() != nil || key == ([32]byte{}) {
		return Sealed{}, pgerrors.ErrInvalid
	}
	if request.OwnerToken != scope.OperationID || request.SourceResourceID != scope.SourceDataResourceID {
		return Sealed{}, pgerrors.ErrConflict
	}
	names := slices.Clone(request.DatabaseNames)
	slices.Sort(names)
	body := payload{Version: 1, Scope: scope, DatabaseNames: names}
	fingerprint, err := fingerprint(body, key)
	if err != nil {
		return Sealed{}, err
	}
	raw, err := json.Marshal(envelope{Payload: body, Key: key, Fingerprint: fingerprint})
	if err != nil {
		return Sealed{}, pgerrors.ErrInvalid
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return Sealed{}, pgerrors.ErrQuotaExceeded
	}
	ciphertext, err := secretbox.SealBytes(recipient, namespace, raw, api.PostgresCopyEnvelopeMaxBytes)
	if err != nil {
		return Sealed{}, pgerrors.ErrUnavailable
	}
	hash := sha256.Sum256(ciphertext)
	sealed := Sealed{Scope: scope, Fingerprint: fingerprint, KeyID: recipient.String(), CiphertextSHA256: hex.EncodeToString(hash[:]), Ciphertext: ciphertext}
	if err := sealed.ValidateMetadata(); err != nil {
		return Sealed{}, err
	}
	return sealed, nil
}

// Recover the committed original selection only. Missing keys, mismatched
// scope or damaged ciphertext never permit a new selection or fingerprint key.
func Open(identities []*age.X25519Identity, expected Scope, sealed Sealed) (Selection, error) {
	if expected.Validate() != nil {
		return Selection{}, pgerrors.ErrInvalid
	}
	if err := sealed.ValidateMetadata(); err != nil {
		return Selection{}, err
	}
	if expected != sealed.Scope {
		return Selection{}, pgerrors.ErrConflict
	}
	matching := []*age.X25519Identity{}
	for _, identity := range identities {
		if identity != nil && identity.Recipient().String() == sealed.KeyID {
			matching = append(matching, identity)
		}
	}
	if len(matching) == 0 {
		return Selection{}, pgerrors.ErrUnavailable
	}
	actualNamespace, raw, err := secretbox.OpenBytesMulti(matching, sealed.Ciphertext)
	if err != nil || actualNamespace != namespace {
		return Selection{}, pgerrors.ErrConflict
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return Selection{}, pgerrors.ErrQuotaExceeded
	}
	var actual envelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&actual) != nil || decoder.Decode(new(any)) != io.EOF || actual.Payload.Scope != expected || actual.Fingerprint != sealed.Fingerprint {
		return Selection{}, pgerrors.ErrConflict
	}
	fingerprint, err := fingerprint(actual.Payload, actual.Key)
	if err != nil || !hmac.Equal([]byte(fingerprint), []byte(actual.Fingerprint)) {
		return Selection{}, pgerrors.ErrConflict
	}
	return Selection{body: actual.Payload, fingerprint: fingerprint}, nil
}

func validPayload(body payload) bool {
	r := managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{
		OwnerToken: body.Scope.OperationID, SourceResourceID: body.Scope.SourceDataResourceID}, DatabaseNames: body.DatabaseNames}
	return body.Version == 1 && body.Scope.Validate() == nil && r.Validate() == nil && slices.IsSorted(body.DatabaseNames) &&
		!slices.Contains(body.DatabaseNames, connectionfence.MaintenanceDatabase)
}

func fingerprint(body payload, key [32]byte) (string, error) {
	if !validPayload(body) || key == ([32]byte{}) {
		return "", pgerrors.ErrInvalid
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", pgerrors.ErrInvalid
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return "", pgerrors.ErrQuotaExceeded
	}
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func hexDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil && hex.EncodeToString(raw) == value
}
