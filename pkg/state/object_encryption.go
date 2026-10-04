package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectEncryptionSnapshot captures private dispatch identity. It belongs in
// the write journal, never in a customer response or a customer-supplied DTO.
type ObjectEncryptionSnapshot struct {
	AccountID     string               `json:"account_id,omitempty"`
	Selection     api.ObjectEncryption `json:"selection"`
	ProviderKeyID string               `json:"provider_key_id,omitempty"`
	KeyIdentity   string               `json:"key_identity,omitempty"`
}

func (e ObjectEncryptionSnapshot) Empty() bool {
	return e.AccountID == "" && e.Selection.Empty() && e.ProviderKeyID == "" && e.KeyIdentity == ""
}

func (e ObjectEncryptionSnapshot) ValidFor(account string) bool {
	if e.Empty() {
		return true
	}
	id, err := uuid.Parse(account)
	if err != nil || id == uuid.Nil || e.AccountID != id.String() || e.Selection.Empty() || !e.Selection.Valid() {
		return false
	}
	if e.Selection.Algorithm == "AES256" {
		return e.ProviderKeyID == "" && e.KeyIdentity == ""
	}
	parts := strings.Split(e.Selection.KeyID, ":")
	return len(parts) == 6 && parts[4] == id.String() &&
		(e.Selection.Algorithm != "aws:kms" || e.Selection.BucketKeyEnabled != nil) &&
		e.ProviderKeyID != "" && len(e.ProviderKeyID) <= api.MaxObjectEncryptionKeyRefBytes && utf8.ValidString(e.ProviderKeyID) &&
		!regexp.MustCompile(`[\x00-\x1f\x7f]`).MatchString(e.ProviderKeyID) &&
		regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(e.KeyIdentity) && e.KeyIdentity != strings.Repeat("0", 64)
}

func (e ObjectEncryptionSnapshot) Clone() ObjectEncryptionSnapshot {
	e.Selection = cloneEncryptionSelection(e.Selection)
	return e
}

func (e ObjectEncryptionSnapshot) Equal(other ObjectEncryptionSnapshot) bool {
	return e.AccountID == other.AccountID && e.ProviderKeyID == other.ProviderKeyID && e.KeyIdentity == other.KeyIdentity && equalEncryptionSelection(e.Selection, other.Selection)
}

// Proof binds native metadata to the complete admitted selection, including
// ownership and the immutable provider enrollment fingerprint.
func (e ObjectEncryptionSnapshot) Proof() string {
	bucketKey := ""
	if e.Selection.BucketKeyEnabled != nil {
		bucketKey = fmt.Sprint(*e.Selection.BucketKeyEnabled)
	}
	sum := sha256.Sum256([]byte(e.AccountID + "\x00" + e.Selection.Algorithm + "\x00" + e.Selection.KeyID + "\x00" + bucketKey + "\x00" + e.Selection.Context + "\x00" + e.ProviderKeyID + "\x00" + e.KeyIdentity))
	return hex.EncodeToString(sum[:])
}

func cloneEncryptionSelection(e api.ObjectEncryption) api.ObjectEncryption {
	if e.BucketKeyEnabled != nil {
		value := *e.BucketKeyEnabled
		e.BucketKeyEnabled = &value
	}
	return e
}

func equalEncryptionSelection(a, b api.ObjectEncryption) bool {
	return a.Algorithm == b.Algorithm && a.KeyID == b.KeyID && a.Context == b.Context &&
		(a.BucketKeyEnabled == nil && b.BucketKeyEnabled == nil || a.BucketKeyEnabled != nil && b.BucketKeyEnabled != nil && *a.BucketKeyEnabled == *b.BucketKeyEnabled)
}

func encryptionSnapshotJSON(e ObjectEncryptionSnapshot) ([]byte, error) {
	if e.Empty() {
		return []byte("{}"), nil
	}
	if !e.ValidFor(e.AccountID) {
		return nil, ErrConflict
	}
	data, err := json.Marshal(e)
	if err != nil || len(data) > api.MaxObjectEncryptionSnapshotBytes {
		return nil, ErrConflict
	}
	return data, nil
}

func encryptionSnapshotFromJSON(data []byte, account string) (ObjectEncryptionSnapshot, error) {
	var e ObjectEncryptionSnapshot
	if trimmed := bytes.TrimSpace(data); len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return e, ErrConflict
	}
	if len(data) == 0 || len(data) > api.MaxObjectEncryptionSnapshotBytes || !utf8.Valid(data) {
		return e, ErrConflict
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil || !e.ValidFor(account) {
		return ObjectEncryptionSnapshot{}, ErrConflict
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ObjectEncryptionSnapshot{}, ErrConflict
	}
	return e, nil
}

func cloneObjectUploadCompletion(c ObjectUploadCompletion) ObjectUploadCompletion {
	c.Encryption = c.Encryption.Clone()
	c.VerifiedEncryption = cloneEncryptionSelection(c.VerifiedEncryption)
	return c
}

func validTrackedEncryptionResult(old, c ObjectUploadCompletion) bool {
	return old.EncryptionDefaultRevision == c.EncryptionDefaultRevision && old.Encryption.Equal(c.Encryption) && old.Encryption.ValidFor(old.AccountID) &&
		(c.Status != "completed" && c.VerifiedEncryption.Empty() || c.Status == "completed" && equalEncryptionSelection(old.Encryption.Selection, c.VerifiedEncryption))
}
