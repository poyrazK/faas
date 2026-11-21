// Package runfile defines metadata shared by stored Run files. Ref is sealed
// internal control-plane data; Descriptor is safe to send to the guest.
package runfile

import (
	"encoding/hex"
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Descriptor struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256,omitempty"`
	Bundle    bool   `json:"bundle,omitempty"`
}

// Ref must never appear in public receipts, logs or guest JSON.
type Ref struct {
	Descriptor
	StorageKey string `json:"storage_key"`
	PrivateKey string `json:"private_key"`
	SealedKey  []byte `json:"sealed_key"`
}

func ValidID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// ValidOwnerID also accepts MemStore's 32-character UUID representation.
// Storage scope remains an exact account string comparison, with no aliases.
func ValidOwnerID(value string) bool {
	if ValidID(value) {
		return true
	}
	if len(value) != 32 {
		return false
	}
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && strings.ReplaceAll(id.String(), "-", "") == value
}
func ValidDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	raw := strings.TrimPrefix(value, "sha256:")
	b, err := hex.DecodeString(raw)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == raw
}
func Key(accountID, id, attempt string) string {
	return "execution-files/v1/" + accountID + "/" + id + "/" + attempt + ".age"
}
func ValidateKey(accountID, id, key string) error {
	parts := strings.Split(key, "/")
	if !ValidOwnerID(accountID) || !ValidID(id) || len(parts) != 5 || parts[0] != "execution-files" || parts[1] != "v1" || parts[2] != accountID || parts[3] != id || !strings.HasSuffix(parts[4], ".age") || !ValidID(strings.TrimSuffix(parts[4], ".age")) {
		return errors.New("invalid Run file storage scope")
	}
	return nil
}
func ValidPath(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0 && path.Clean(value) == value && !strings.HasPrefix(value, "/") && value != "." && value != ".." && !strings.HasPrefix(value, "../") && !strings.ContainsAny(value, "\\:")
}
