// Package runtimefence verifies private provider-independent attestations of
// irreversible host-epoch termination. A signature authenticates a reviewed
// authority's assertion, not a local observer or a provider's enforcement.
package runtimefence

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"filippo.io/edwards25519"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrUnverified = errors.New("external host epoch fence unverified")

const Contract = "irreversible_host_epoch_termination_v1"
const SignatureDomain = "gregale.runtime-upgrade.external-fence.v1\x00"

// Intent is frozen administrative attribution. It must come from the locked
// database review, never from the attestation or an unreachable process.
type Intent struct {
	ID              string `json:"id"`
	WithdrawalID    string `json:"withdrawal_id"`
	AuthorityID     string `json:"authority_id"`
	Challenge       string `json:"challenge"`
	GatewayRevision string `json:"gateway_revision"`
	PublicRevision  string `json:"public_revision"`
	SlotID          string `json:"slot_id"`
	SessionID       string `json:"session_id"`
	ConfigSHA256    string `json:"config_sha256"`
	MachineID       string `json:"machine_id"`
	BootID          string `json:"boot_id"`
	ResourceID      string `json:"resource_id"`
	ScopeSHA256     string `json:"scope_sha256"`
	CreatedAtMicros int64  `json:"created_at_micros"`
}

// Claim has one supported contract: the entire host execution epoch, including
// all processes, namespaces, activation, connections and memory/snapshot resume,
// is irreversibly terminated. Temporary off/firewall/mask states are ineligible.
type Claim struct {
	Version          int    `json:"version"`
	Contract         string `json:"contract"`
	AuthorityID      string `json:"authority_id"`
	ReceiptID        string `json:"receipt_id"`
	IntentID         string `json:"intent_id"`
	IntentSHA256     string `json:"intent_sha256"`
	Challenge        string `json:"challenge"`
	EnforcedAtMicros int64  `json:"enforced_at_micros"`
	IssuedAtMicros   int64  `json:"issued_at_micros"`
}

// Envelope is canonical encoding/json output in this field order, without
// whitespace. The Ed25519 message is SignatureDomain followed by Marshal(Claim).
// Signature uses unpadded base64url. The platform has no signing implementation.
type Envelope struct {
	Claim     Claim  `json:"claim"`
	Signature string `json:"signature"`
}

type Verifier struct {
	authority string
	key       ed25519.PublicKey
}

// VerifiedReceipt cannot be built from a caller's booleans or an unsigned DTO.
// The database still re-verifies against its own locked intent and pinned key.
type VerifiedReceipt struct {
	claim    Claim
	envelope []byte
	digest   string
}

func (r VerifiedReceipt) Claim() Claim     { return r.claim }
func (r VerifiedReceipt) Envelope() []byte { return slices.Clone(r.envelope) }
func (r VerifiedReceipt) SHA256() string   { return r.digest }

func NewVerifier(authority string, publicKey []byte) (*Verifier, error) {
	if !CanonicalID(authority) || len(publicKey) != ed25519.PublicKeySize {
		return nil, ErrUnverified
	}
	point, err := new(edwards25519.Point).SetBytes(publicKey)
	if err != nil || !bytes.Equal(point.Bytes(), publicKey) || new(edwards25519.Point).MultByCofactor(point).Equal(edwards25519.NewIdentityPoint()) == 1 {
		return nil, ErrUnverified
	}
	return &Verifier{authority: authority, key: slices.Clone(publicKey)}, nil
}

func (v *Verifier) Verify(intent Intent, now time.Time, raw []byte) (VerifiedReceipt, error) {
	digest, err := IntentDigest(intent)
	if err != nil || v == nil || v.authority != intent.AuthorityID || len(v.key) != ed25519.PublicKeySize || now.IsZero() || len(raw) == 0 || len(raw) > api.RuntimeUpgradeExternalFenceEnvelopeMaxBytes {
		return VerifiedReceipt{}, ErrUnverified
	}
	// Freeze before decoding, hashing or retaining the caller-owned bytes.
	raw = slices.Clone(raw)
	var envelope Envelope
	if json.Unmarshal(raw, &envelope) != nil {
		return VerifiedReceipt{}, ErrUnverified
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(canonical, raw) {
		// Also rejects duplicate/unknown keys, reordered fields, missing fields,
		// trailing values, alternate escapes and permissive decoder case aliases.
		return VerifiedReceipt{}, ErrUnverified
	}
	c := envelope.Claim
	if c.Version != 1 || c.Contract != Contract || c.AuthorityID != v.authority || !CanonicalID(c.ReceiptID) || c.IntentID != intent.ID || c.IntentSHA256 != digest || c.Challenge != intent.Challenge || c.EnforcedAtMicros < intent.CreatedAtMicros || c.IssuedAtMicros < c.EnforcedAtMicros || c.IssuedAtMicros > now.UnixMicro() || now.UnixMicro()-c.IssuedAtMicros > api.RuntimeUpgradeExternalFenceMaxAge.Microseconds() {
		return VerifiedReceipt{}, ErrUnverified
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawURLEncoding.EncodeToString(signature) != envelope.Signature {
		return VerifiedReceipt{}, ErrUnverified
	}
	payload, err := json.Marshal(c)
	if err != nil || !ed25519.Verify(v.key, append([]byte(SignatureDomain), payload...), signature) {
		return VerifiedReceipt{}, ErrUnverified
	}
	hash := sha256.Sum256(raw)
	return VerifiedReceipt{claim: c, envelope: raw, digest: hex.EncodeToString(hash[:])}, nil
}

func IntentDigest(intent Intent) (string, error) {
	for _, id := range []string{intent.ID, intent.WithdrawalID, intent.AuthorityID, intent.Challenge, intent.GatewayRevision, intent.PublicRevision, intent.SlotID, intent.SessionID, intent.BootID} {
		if !CanonicalID(id) {
			return "", ErrUnverified
		}
	}
	if !ValidHostScope(intent.MachineID, intent.BootID, intent.ResourceID, intent.ScopeSHA256) || !Digest(intent.ConfigSHA256) || intent.CreatedAtMicros < 1 {
		return "", ErrUnverified
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return "", ErrUnverified
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func CanonicalID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func Digest(value string) bool { return len(value) == hex.EncodedLen(sha256.Size) && lowerHex(value) }

func ValidHostScope(machine, boot, resource, scope string) bool {
	return len(machine) == 32 && machine != strings.Repeat("0", 32) && lowerHex(machine) && CanonicalID(boot) && ResourceID(resource) && Digest(scope)
}

func lowerHex(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) == -1
}

func ResourceID(value string) bool {
	return len(value) > 0 && len(value) <= api.RuntimeUpgradeExternalFenceResourceMaxBytes && strings.IndexFunc(value, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && !strings.ContainsRune("._:/@-", r)
	}) == -1
}
