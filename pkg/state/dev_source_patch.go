package state

import (
	"context"
	"time"
)

// DevSourceEntry is one entry of a developer source archive, as recorded for
// ADR-740 live patches. Type is the tar typeflag; Digest is the content
// SHA-256 for regular files.
type DevSourceEntry struct {
	Type   byte   `json:"t"`
	Mode   int64  `json:"m"`
	Size   int64  `json:"s"`
	Digest string `json:"d,omitempty"`
}

// DevSourceManifest is the complete source a developer deployment was built
// from. A live patch is the difference between the live deployment's
// manifest and the newest synced source.
type DevSourceManifest struct {
	DeploymentID string
	AppID        string
	SourceRoot   string
	Entries      map[string]DevSourceEntry
	CreatedAt    time.Time
}

// DevSourcePatch is one complete patch against BaseDeploymentID's source:
// Archive is a tar.gz of the added or changed files and Deleted lists removed
// paths, all relative to the source root. Generation increases per base
// deployment.
type DevSourcePatch struct {
	ID               string
	AppID            string
	BaseDeploymentID string
	Generation       int64
	ImageDir         string
	Archive          []byte
	Deleted          []string
	Digest           string
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

// DevSourcePatchStore is implemented by PgStore and MemStore. apid records
// manifests and patches; vmmd reads the newest patch for an instance.
type DevSourcePatchStore interface {
	// RecordDevSourceManifest stores the manifest and prunes the app's older
	// manifests, keeping the newest keep plus the live deployment's.
	RecordDevSourceManifest(ctx context.Context, manifest DevSourceManifest, keep int) error
	DevSourceManifest(ctx context.Context, deploymentID string) (DevSourceManifest, error)
	// CreateDevSourcePatch assigns the next generation for the base
	// deployment and drops the app's patches for any other base deployment.
	CreateDevSourcePatch(ctx context.Context, patch DevSourcePatch) (DevSourcePatch, error)
	// LatestDevSourcePatch returns the newest unexpired patch with a
	// generation above afterGeneration, or ErrNotFound.
	LatestDevSourcePatch(ctx context.Context, appID, baseDeploymentID string, afterGeneration int64) (DevSourcePatch, error)
}
