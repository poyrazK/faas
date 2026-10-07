package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ObjectUploadGrantPut           = "put"
	ObjectUploadGrantMultipartPart = "multipart_part"
)

// Broker grants authorize an exact PUT through the Gregale gateway. They do
// not expose provider capabilities or create an outstanding native grant.
// Only the token hash is persisted; active writers are tracked separately.
type ObjectUploadGrant struct {
	ID, TokenHash, Kind, Key   string
	Bucket                     ObjectBucket
	SizeBytes                  int64
	Headers                    map[string]string
	UploadID, ProviderUploadID string
	PartNumber                 int32
	CreatedAt, ExpiresAt       time.Time
}

type ObjectUploadGrantStore interface {
	CreateObjectUploadGrant(context.Context, ObjectUploadGrant, int) (ObjectUploadGrant, error)
	ResolveObjectUploadGrant(context.Context, string) (ObjectUploadGrant, error)
	PruneExpiredObjectUploadGrants(context.Context, int32) (int64, error)
}

func validObjectUploadGrant(g ObjectUploadGrant, ttl int) bool {
	if !validObjectMutationBucket(g.Bucket) || !validObjectMutationToken(g.ID) || !validObjectUploadTokenHash(g.TokenHash) ||
		ttl < 1 || ttl > api.MaxObjectSignedURLExpiresSeconds || g.SizeBytes < 0 || g.SizeBytes > api.MaxObjectSinglePutBytes ||
		len(g.Key) < 1 || len(g.Key) > 1024 || !utf8.ValidString(g.Key) {
		return false
	}
	for _, r := range g.Key {
		if r < 32 || r == 127 {
			return false
		}
	}
	if g.Headers == nil || g.Headers["Content-Length"] != strconv.FormatInt(g.SizeBytes, 10) {
		return false
	}
	for name, value := range g.Headers {
		if http.CanonicalHeaderKey(name) != name || !objectUploadGrantHeader(name) || strings.ContainsAny(value, "\r\n\x00") {
			return false
		}
	}
	data, err := json.Marshal(g.Headers)
	if err != nil || len(data) > api.ObjectUploadGrantMaxHeaderBytes {
		return false
	}
	switch g.Kind {
	case ObjectUploadGrantPut:
		return g.UploadID == "" && g.ProviderUploadID == "" && g.PartNumber == 0
	case ObjectUploadGrantMultipartPart:
		return validObjectMutationToken(g.UploadID) && g.ProviderUploadID != "" && g.SizeBytes > 0 && g.PartNumber >= 1 && g.PartNumber <= api.MaxMultipartParts
	default:
		return false
	}
}

// Content-Length is checked against Request.ContentLength. The remaining
// fields are the portable metadata surface accepted by the object API.
func objectUploadGrantHeader(name string) bool {
	switch name {
	case "Content-Length", "Content-Type", "Cache-Control", "Content-Disposition", "Content-Encoding", "Content-Language", "X-Amz-Tagging":
		return true
	}
	return strings.HasPrefix(name, "X-Amz-Meta-") && len(name) > len("X-Amz-Meta-")
}

func cloneObjectUploadGrant(g ObjectUploadGrant) ObjectUploadGrant {
	g.Headers = maps.Clone(g.Headers)
	return g
}

func validObjectUploadTokenHash(hash string) bool {
	decoded, err := hex.DecodeString(hash)
	return err == nil && len(decoded) == 32 && strings.ToLower(hash) == hash
}

func validObjectUploadGrantPart(g ObjectUploadGrant, u ObjectMultipartUpload, now time.Time) bool {
	if u.ID != g.UploadID || u.AccountID != g.Bucket.AccountID || u.AppID != g.Bucket.AppID || u.BucketID != g.Bucket.ID ||
		u.Key != g.Key || u.ProviderUploadID != g.ProviderUploadID || u.State != ObjectMultipartActive || !u.ExpiresAt.After(now) ||
		u.PartCount < 1 || g.PartNumber > u.PartCount {
		return false
	}
	size := u.PartSizeBytes
	if g.PartNumber == u.PartCount {
		size = u.SizeBytes - u.PartSizeBytes*int64(u.PartCount-1)
	}
	return size == g.SizeBytes
}
