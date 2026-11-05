package state

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Native identities never appear in customer DTOs. Null is the standard S3
// mutable version; every non-null native identity gets a durable Gregale UUID.
type ObjectVersionIdentity struct {
	ID                string
	Key               string
	ProviderVersionID string `json:"-"`
	DeleteMarker      bool   `json:"-"`
	// GenerationVersion marks ProviderVersionID as a generation every object
	// carries (GCS), not evidence of retained versions (ADR-20261105120000).
	GenerationVersion bool `json:"-"`
}

// ProviderVersionObserved reports whether a provider version ID shows the
// provider keeps noncurrent versions: an S3 ID other than "null" does, a
// generation (generation=true) never does on its own (ADR-20261105120000).
func ProviderVersionObserved(id string, generation bool) bool {
	return !generation && id != "" && id != "null"
}

type ObjectVersionReferenceStore interface {
	RecordObjectVersions(context.Context, string, string, []ObjectVersionIdentity) ([]ObjectVersionIdentity, error)
	ResolveObjectVersion(context.Context, string, string, string, string) (string, error)
}

func ValidObjectVersionID(id string) bool {
	if id == "null" {
		return true
	}
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id && u.Version() == 4 && u.Variant() == uuid.RFC4122
}

func validVersionReferenceText(value string, max int) bool {
	if len(value) == 0 || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func validVersionReferences(items []ObjectVersionIdentity) bool {
	if len(items) > api.ObjectVersionReferenceBatchMax {
		return false
	}
	seen := map[string]bool{}
	for _, v := range items {
		if !validVersionReferenceText(v.Key, api.MaxObjectS3ListTextBytes) || !validVersionReferenceText(v.ProviderVersionID, api.ObjectProviderVersionIDMaxBytes) {
			return false
		}
		identity := v.Key + "\x00" + v.ProviderVersionID
		if seen[identity] {
			return false
		}
		seen[identity] = true
	}
	return true
}

func versionReferenceIdentity(bucket string, v ObjectVersionIdentity) string {
	return strings.Join([]string{bucket, v.Key, v.ProviderVersionID}, "\x00")
}
