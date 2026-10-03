package copyinventory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const sealedNamespace = "gregale-postgres-copy-inventory-v1"

// Scope is derived from the frozen clone and its authenticated snapshot/fork
// receipts. It is authenticated inside the ciphertext as well as stored beside
// it. It cannot authorize a provider connection or establish copy readiness.
type Scope struct {
	PostgresMajor                                                          int
	OperationID, AccountID, ProjectID, SourceDatabaseID, CaptureDatabaseID string
	SourceVersion, BackendID, BackendFingerprint                           string
	SourceProviderResourceID, SourceDataResourceID, ProviderSnapshotID     string
	CaptureProviderResourceID                                              string
	CapturePoint, SnapshotCreatedAt, CaptureCreatedAt                      time.Time
}

func (s Scope) Validate() error {
	if s.PostgresMajor < 14 || s.PostgresMajor > 99 {
		return managedpostgres.ErrInvalid
	}
	for _, id := range []string{s.OperationID, s.AccountID, s.ProjectID, s.SourceDatabaseID, s.CaptureDatabaseID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return managedpostgres.ErrInvalid
		}
	}
	for _, id := range []string{s.BackendID, s.SourceProviderResourceID, s.SourceDataResourceID, s.ProviderSnapshotID, s.CaptureProviderResourceID} {
		if id == "" || len(id) > 255 || strings.ContainsRune(id, 0) {
			return managedpostgres.ErrInvalid
		}
	}
	if !hexDigest(s.SourceVersion) || !hexDigest(s.BackendFingerprint) || s.CaptureDatabaseID == s.SourceDatabaseID ||
		s.CaptureProviderResourceID == s.SourceProviderResourceID || s.CaptureProviderResourceID == s.SourceDataResourceID {
		return managedpostgres.ErrInvalid
	}
	for _, at := range []time.Time{s.CapturePoint, s.SnapshotCreatedAt, s.CaptureCreatedAt} {
		if at.IsZero() || at.Year() < 1 || at.Year() > 9999 || at.Nanosecond()%1000 != 0 {
			return managedpostgres.ErrInvalid
		}
	}
	if s.SnapshotCreatedAt.Before(s.CapturePoint) || s.CaptureCreatedAt.Before(s.SnapshotCreatedAt) {
		return managedpostgres.ErrInvalid
	}
	return nil
}

func (s Scope) Equal(other Scope) bool {
	s.CapturePoint, s.SnapshotCreatedAt, s.CaptureCreatedAt = s.CapturePoint.UTC(), s.SnapshotCreatedAt.UTC(), s.CaptureCreatedAt.UTC()
	other.CapturePoint, other.SnapshotCreatedAt, other.CaptureCreatedAt = other.CapturePoint.UTC(), other.SnapshotCreatedAt.UTC(), other.CaptureCreatedAt.UTC()
	return s == other
}

// Sealed exposes only non-secret receipt metadata in JSON/formatted output.
// Ciphertext is explicit private storage input; SQL names, config values, SQL
// identity pins and the fingerprint key exist only inside its age envelope.
type Sealed struct {
	Scope              Scope
	Fingerprint, KeyID string
	CiphertextSHA256   string
	Ciphertext         []byte `json:"-"`
}

func (s Sealed) String() string {
	return fmt.Sprintf("sealed copy inventory: fingerprint=%s", s.Fingerprint)
}
func (s Sealed) GoString() string { return s.String() }

func (s Sealed) ValidateMetadata() error {
	if err := s.Scope.Validate(); err != nil {
		return err
	}
	if len(s.Ciphertext) > api.PostgresCopyCiphertextMaxBytes {
		return managedpostgres.ErrQuotaExceeded
	}
	key, err := age.ParseX25519Recipient(s.KeyID)
	if err != nil || key.String() != s.KeyID || len(s.Ciphertext) == 0 || !hexDigest(s.Fingerprint) || !hexDigest(s.CiphertextSHA256) {
		return managedpostgres.ErrInvalid
	}
	hash := sha256.Sum256(s.Ciphertext)
	if hex.EncodeToString(hash[:]) != s.CiphertextSHA256 {
		return managedpostgres.ErrConflict
	}
	return nil
}

type privateEnvelope struct {
	Version     int             `json:"version"`
	Scope       Scope           `json:"scope"`
	Config      Config          `json:"config"`
	Fingerprint string          `json:"fingerprint"`
	Payload     json.RawMessage `json:"payload"`
}

// SealInventory binds the private key/config and payload to an exact frozen
// scope. The caller must authenticate the SQL connection before Read.
func SealInventory(recipient *age.X25519Recipient, scope Scope, cfg Config, inventory Inventory) (Sealed, error) {
	if recipient == nil || scope.Validate() != nil || !validConfig(cfg) {
		return Sealed{}, managedpostgres.ErrInvalid
	}
	if cfg.PostgresMajor != scope.PostgresMajor {
		return Sealed{}, managedpostgres.ErrConflict
	}
	raw, err := inventory.PayloadForSealing()
	if err != nil {
		return Sealed{}, err
	}
	if _, err := RecoverPrivatePayload(raw, cfg, inventory.fingerprint); err != nil {
		return Sealed{}, err
	}
	envelope, err := json.Marshal(privateEnvelope{Version: 1, Scope: scope, Config: cfg, Fingerprint: inventory.fingerprint, Payload: raw})
	if err != nil {
		return Sealed{}, managedpostgres.ErrInvalid
	}
	if len(envelope) > api.PostgresCopyEnvelopeMaxBytes {
		return Sealed{}, managedpostgres.ErrQuotaExceeded
	}
	ciphertext, err := secretbox.SealBytes(recipient, sealedNamespace, envelope, api.PostgresCopyEnvelopeMaxBytes)
	if err != nil {
		return Sealed{}, managedpostgres.ErrUnavailable
	}
	hash := sha256.Sum256(ciphertext)
	sealed := Sealed{Scope: scope, Fingerprint: inventory.fingerprint, KeyID: recipient.String(), CiphertextSHA256: hex.EncodeToString(hash[:]), Ciphertext: ciphertext}
	return sealed, sealed.ValidateMetadata()
}

// OpenInventory recovers only committed private metadata, with current/previous
// host identities. It neither reconnects to a source nor changes the data point.
func OpenInventory(identities []*age.X25519Identity, expected Scope, sealed Sealed) (Inventory, error) {
	if expected.Validate() != nil {
		return Inventory{}, managedpostgres.ErrInvalid
	}
	if err := sealed.ValidateMetadata(); err != nil {
		return Inventory{}, err
	}
	if !expected.Equal(sealed.Scope) {
		return Inventory{}, managedpostgres.ErrConflict
	}
	matching := false
	for _, identity := range identities {
		if identity != nil && identity.Recipient().String() == sealed.KeyID {
			matching = true
		}
	}
	if !matching {
		return Inventory{}, managedpostgres.ErrUnavailable
	}
	namespace, raw, err := secretbox.OpenBytesMulti(identities, sealed.Ciphertext)
	if err != nil || namespace != sealedNamespace {
		return Inventory{}, managedpostgres.ErrConflict
	}
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return Inventory{}, managedpostgres.ErrQuotaExceeded
	}
	var envelope privateEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(new(any)) != io.EOF || envelope.Version != 1 ||
		!expected.Equal(envelope.Scope) || envelope.Fingerprint != sealed.Fingerprint || envelope.Config.PostgresMajor != expected.PostgresMajor {
		return Inventory{}, managedpostgres.ErrConflict
	}
	return RecoverPrivatePayload(envelope.Payload, envelope.Config, sealed.Fingerprint)
}

func hexDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil && hex.EncodeToString(raw) == value
}
