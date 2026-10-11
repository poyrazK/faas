package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ProjectEnvironmentCloneObjectManifest is the durable set of exact source
// versions selected for one isolated bucket. It is committed before copying.
type ProjectEnvironmentCloneObjectManifest struct {
	OperationID    string
	SourceBucketID string
	TargetBucketID string
	CapturedAt     time.Time
	Hash           string
	Objects        []ProjectEnvironmentCloneObjectCheckpoint
}

type ProjectEnvironmentCloneObjectCheckpoint struct {
	Source         ProjectEnvironmentCloneObjectVersion
	CopiedAt       *time.Time
	TargetETag     string
	VerifiedSHA256 string
}

// ProjectEnvironmentCloneObjectVersion mirrors the provider-neutral manifest
// wire fields without making state depend on objectstorage (which imports state).
type ProjectEnvironmentCloneObjectVersion struct {
	Key             string    `json:"key"`
	VersionID       string    `json:"version_id"`
	MetadataVersion string    `json:"metadata_version,omitempty"`
	Size            int64     `json:"size_bytes"`
	ETag            string    `json:"etag,omitempty"`
	LastModified    time.Time `json:"last_modified"`
	ValidUntil      time.Time `json:"valid_until,omitempty"`
	Deleted         bool      `json:"deleted,omitempty"`
}

type ProjectEnvironmentCloneObjectManifestStore interface {
	PutProjectEnvironmentCloneObjectManifest(context.Context, string, string, ProjectEnvironmentCloneObjectManifest) (ProjectEnvironmentCloneObjectManifest, error)
	ProjectEnvironmentCloneObjectManifest(context.Context, string, string, string, string) (ProjectEnvironmentCloneObjectManifest, error)
	MarkProjectEnvironmentCloneObjectCopied(context.Context, string, string, string, string, string, string, string, string) error
}

// Claimed operations use explicit worker authority for manifest/checkpoint
// writes. The legacy writer methods are restricted to never-claimed captures.
type ProjectEnvironmentCloneLeasedObjectManifestStore interface {
	ProjectEnvironmentCloneObjectManifestStore
	PutProjectEnvironmentCloneObjectManifestForLease(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneObjectManifest) (ProjectEnvironmentCloneObjectManifest, error)
	MarkProjectEnvironmentCloneObjectCopiedForLease(context.Context, ProjectEnvironmentCloneLease, string, string, string, string, string) error
}

func validateProjectEnvironmentCloneObjectManifest(manifest ProjectEnvironmentCloneObjectManifest) error {
	for _, id := range []string{manifest.OperationID, manifest.SourceBucketID, manifest.TargetBucketID} {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalidProjectEnvironmentCloneOperation
		}
	}
	if manifest.SourceBucketID == manifest.TargetBucketID || manifest.CapturedAt.IsZero() {
		return ErrInvalidProjectEnvironmentCloneOperation
	}
	items := make([]ProjectEnvironmentCloneObjectVersion, len(manifest.Objects))
	for i, checkpoint := range manifest.Objects {
		item := checkpoint.Source
		if checkpoint.CopiedAt != nil || checkpoint.TargetETag != "" || checkpoint.VerifiedSHA256 != "" ||
			item.Size < 0 || item.LastModified.IsZero() || item.LastModified.After(manifest.CapturedAt) ||
			(!item.ValidUntil.IsZero() && !item.ValidUntil.After(manifest.CapturedAt)) {
			return ErrInvalidProjectEnvironmentCloneOperation
		}
		items[i] = item
	}
	hash, err := ProjectEnvironmentCloneObjectManifestHash(items)
	if err != nil || hash != manifest.Hash {
		return ErrInvalidProjectEnvironmentCloneOperation
	}
	return nil
}

func ProjectEnvironmentCloneObjectManifestHash(items []ProjectEnvironmentCloneObjectVersion) (string, error) {
	for i, item := range items {
		if !validCloneObjectKey(item.Key) || item.VersionID == "" || item.Deleted ||
			(i > 0 && items[i-1].Key >= item.Key) {
			return "", ErrInvalidProjectEnvironmentCloneOperation
		}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func validCloneObjectKey(key string) bool {
	if key == "" || len(key) > 1024 || !utf8.ValidString(key) {
		return false
	}
	for _, c := range key {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func validCloneObjectSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func cloneProjectEnvironmentCloneObjectManifest(manifest ProjectEnvironmentCloneObjectManifest) ProjectEnvironmentCloneObjectManifest {
	manifest.Objects = append([]ProjectEnvironmentCloneObjectCheckpoint(nil), manifest.Objects...)
	for i := range manifest.Objects {
		if manifest.Objects[i].CopiedAt != nil {
			at := *manifest.Objects[i].CopiedAt
			manifest.Objects[i].CopiedAt = &at
		}
	}
	return manifest
}
