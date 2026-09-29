package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// ObjectVersion identifies immutable object bytes in a versioned bucket. A
// delete marker records that a key did not exist at a capture point.
type ObjectVersion struct {
	Key             string    `json:"key"`
	VersionID       string    `json:"version_id"`
	MetadataVersion string    `json:"metadata_version,omitempty"`
	Size            int64     `json:"size_bytes"`
	ETag            string    `json:"etag,omitempty"`
	LastModified    time.Time `json:"last_modified"`
	ValidUntil      time.Time `json:"valid_until,omitempty"`
	Deleted         bool      `json:"deleted,omitempty"`
}

type ObjectVersionPage struct {
	Items      []ObjectVersion
	NextCursor string
}

// VersionedObjectLister must expose all retained versions and deletion events,
// not just live keys. BucketVersioningEnabled is checked before capture; a
// caller must enable version retention before choosing the capture time.
type VersionedObjectLister interface {
	BucketVersioningEnabled(context.Context, string) (bool, error)
	ListObjectVersions(context.Context, string, string, int32) (ObjectVersionPage, error)
}

var ErrAmbiguousObjectSnapshot = errors.New("object storage: ambiguous object versions at capture time")

// CaptureObjectManifest reconstructs the live key set at a point in time. A
// bucket's version retention must have been active before asOf. Same-timestamp
// changes to one key are rejected because provider timestamp granularity may
// be too coarse to establish their order across pages and delete markers.
func CaptureObjectManifest(ctx context.Context, provider VersionedObjectLister, bucket string, asOf time.Time, maxPages int) ([]ObjectVersion, error) {
	if provider == nil || bucket == "" || asOf.IsZero() || maxPages <= 0 {
		return nil, ErrInvalid
	}
	enabled, err := provider.BucketVersioningEnabled(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrUnsupported
	}
	latest := make(map[string]ObjectVersion)
	cursor := ""
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		page, err := provider.ListObjectVersions(ctx, bucket, cursor, 1000)
		if err != nil {
			return nil, err
		}
		if len(page.Items) > 1000 {
			return nil, ErrInvalid
		}
		for _, item := range page.Items {
			if !ValidKey(item.Key) || item.VersionID == "" || item.Size < 0 || item.LastModified.IsZero() {
				return nil, ErrInvalid
			}
			if item.LastModified.After(asOf) || (!item.ValidUntil.IsZero() && !item.ValidUntil.After(asOf)) {
				continue
			}
			previous, found := latest[item.Key]
			if !found || item.LastModified.After(previous.LastModified) {
				latest[item.Key] = item
			} else if item.LastModified.Equal(previous.LastModified) && item.VersionID != previous.VersionID {
				return nil, ErrAmbiguousObjectSnapshot
			}
		}
		if page.NextCursor == "" {
			manifest := make([]ObjectVersion, 0, len(latest))
			for _, item := range latest {
				if !item.Deleted {
					manifest = append(manifest, item)
				}
			}
			sort.Slice(manifest, func(i, j int) bool { return manifest[i].Key < manifest[j].Key })
			return manifest, nil
		}
		if len(page.NextCursor) > 8192 || page.NextCursor == cursor || seen[page.NextCursor] {
			return nil, ErrInvalid
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return nil, ErrUnavailable
}

// ObjectManifestHash pins the ordered, non-secret object identities in a
// clone receipt. The manifest is sorted by CaptureObjectManifest.
func ObjectManifestHash(manifest []ObjectVersion) (string, error) {
	for i, item := range manifest {
		if !ValidKey(item.Key) || item.VersionID == "" || item.Deleted || (i > 0 && manifest[i-1].Key >= item.Key) {
			return "", ErrInvalid
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
