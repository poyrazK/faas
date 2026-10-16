package promotionrollout

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrInvalid = errors.New("invalid promotion rollout evidence")

func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func validHex(value string, size int) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == size && hex.EncodeToString(raw) == value
}
func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// Decode rejects duplicate keys at every level, unknown fields and extra JSON.
// This avoids different tools interpreting the same signed bytes differently.
func Decode(raw []byte, out any) error {
	if len(raw) > api.PromotionRolloutApprovalMaxBytes {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 64 {
			return ErrInvalid
		}
		token, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		if delimiter, ok := token.(json.Delim); ok {
			keys := map[string]bool{}
			for d.More() {
				if delimiter == '{' {
					key, err := d.Token()
					name, ok := key.(string)
					if err != nil || !ok || keys[name] {
						return ErrInvalid
					}
					keys[name] = true
				}
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			if _, err := d.Token(); err != nil {
				return ErrInvalid
			}
		}
		return nil
	}
	if visit(0) != nil {
		return ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrInvalid
	}
	canonical, err := json.Marshal(out)
	if err != nil {
		return ErrInvalid
	}
	var original, encoded any
	if json.Unmarshal(raw, &original) != nil || json.Unmarshal(canonical, &encoded) != nil || !sameShape(original, encoded) {
		return ErrInvalid
	}
	return nil
}

// Struct keys must use their exact spelling, with every required field present.
// encoding/json otherwise accepts case aliases and null scalar values.
func sameShape(original, encoded any) bool {
	if original == nil || encoded == nil {
		return original == nil && encoded == nil
	}
	switch value := original.(type) {
	case map[string]any:
		other, ok := encoded.(map[string]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for key, child := range value {
			peer, exists := other[key]
			if !exists || !sameShape(child, peer) {
				return false
			}
		}
	case []any:
		other, ok := encoded.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for index, child := range value {
			if !sameShape(child, other[index]) {
				return false
			}
		}
	}
	return true
}

// ReadFile rejects symlinks, special files, writable shared files and oversized
// inputs. Errors never contain operator paths, report contents or key material.
func ReadFile(path string, maximum int) ([]byte, error) {
	f, err := openRegular(path, maximum)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, int64(maximum)+1))
	if err != nil || len(raw) > maximum {
		return nil, ErrInvalid
	}
	return raw, nil
}

func openRegular(path string, maximum int) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Size() > int64(maximum) {
		return nil, ErrInvalid
	}
	f, err := openReadOnly(path)
	if err != nil {
		return nil, ErrInvalid
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Mode().Perm()&0022 != 0 {
		_ = f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}

func NativeLogMatches(reportRaw []byte, path string) bool {
	var report nativeReport
	if Decode(reportRaw, &report) != nil {
		return false
	}
	f, err := openRegular(path, api.PromotionRolloutNativeLogMaxBytes)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, api.PromotionRolloutNativeLogMaxBytes+1))
	return err == nil && n <= api.PromotionRolloutNativeLogMaxBytes && hex.EncodeToString(hash.Sum(nil)) == report.LogSHA256
}

func PublicKey(raw []byte) (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(string(bytes.TrimSpace(raw)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrInvalid
	}
	return ed25519.PublicKey(key), nil
}

// Sign is an explicit operator approval, not a native test runner. The signer
// must review native artifacts and every supplemental gate before signing.
func Sign(approval Approval, key ed25519.PrivateKey, now time.Time) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize || validateApproval(approval, approval.SourceSHA, now) != "" {
		return nil, ErrInvalid
	}
	for _, capability := range approval.Capabilities {
		if len(missingGates(approval, capability)) != 0 {
			return nil, ErrInvalid
		}
	}
	payload, err := json.Marshal(approval)
	if err != nil {
		return nil, ErrInvalid
	}
	public := key.Public().(ed25519.PublicKey)
	envelope := Envelope{SchemaVersion: Version, KeyID: digest(public), Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(signatureDomain), payload...)))}
	raw, err := json.Marshal(envelope)
	if err != nil || len(raw) > api.PromotionRolloutApprovalMaxBytes {
		return nil, ErrInvalid
	}
	return append(raw, '\n'), nil
}

func verify(raw []byte, public ed25519.PublicKey) (Approval, error) {
	var envelope Envelope
	var approval Approval
	if Decode(raw, &envelope) != nil || envelope.SchemaVersion != Version || len(public) != ed25519.PublicKeySize || envelope.KeyID != digest(public) {
		return approval, ErrInvalid
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if err != nil || base64.StdEncoding.EncodeToString(payload) != envelope.Payload {
		return approval, ErrInvalid
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || !ed25519.Verify(public, append([]byte(signatureDomain), payload...), signature) || Decode(payload, &approval) != nil {
		return Approval{}, ErrInvalid
	}
	return approval, nil
}
