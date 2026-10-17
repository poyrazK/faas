// Package objectmultipart defines the private multipart completion identity
// shared by the storage node and its provider adapter.
package objectmultipart

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

type Part struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
	Checksum   string `json:"checksum,omitempty"`
}

// Manifest binds an ordered selection, conditions and verified checksum to an
// exact byte count. Transport signatures and request timestamps are excluded.
type Manifest struct {
	UploadID    string                `json:"upload_id"`
	Parts       []Part                `json:"parts"`
	Size        int64                 `json:"size"`
	IfMatch     string                `json:"if_match,omitempty"`
	IfNoneMatch string                `json:"if_none_match,omitempty"`
	Checksum    objectchecksum.Digest `json:"checksum"`
}

// NativeUploadID keeps the private identity distinct from the public receipt,
// while making an uncertain initiation safe to repeat on the owned node.
// This is an identity mapping, not an authentication or secrecy mechanism.
func NativeUploadID(receipt string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale-owned-multipart-v1\x00"+receipt)).String()
}

func (m Manifest) Hash() string {
	body, _ := json.Marshal(m) // Fixed JSON-safe DTO.
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
