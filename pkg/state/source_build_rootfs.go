package state

// adr: 435. Source conversion lineage is distinct from registry/native authority.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/ociref"
)

const SourceBuildRootfsLayout = "faas-app-layer-layout-v1"

type SourceBuildRootfsInput struct {
	ID              string `json:"-"`
	PublicationID   string `json:"publication_id"`
	PublicationHash string `json:"publication_hash"`
	AccountID       string `json:"account_id"`
	OrgID           string `json:"org_id"`
	AppID           string `json:"app_id"`
	DeploymentID    string `json:"deployment_id"`
	Scope           string `json:"scope"`
	Kind            string `json:"kind"`
	Runtime         string `json:"runtime"`
	IntentHash      string `json:"intent_hash"`
	StorageKey      string `json:"storage_key"`
	RootfsPath      string `json:"rootfs_path"`
	ContentBytes    int64  `json:"content_bytes"`
	ArtifactDigest  string `json:"artifact_digest"`
	ArtifactBytes   int64  `json:"artifact_bytes"`
	BaseProducerID  string `json:"base_producer_id"`
	BaseInputHash   string `json:"base_input_hash"`
	GuestInitDigest string `json:"guest_init_digest"`
	RunnerDigest    string `json:"runner_digest"`
	LayoutVersion   string `json:"layout_version"`
}

type SourceBuildRootfs struct {
	ID, InputHash          string
	Input                  SourceBuildRootfsInput
	PublishedAt, ExpiresAt time.Time
}

type SourceBuildRootfsStore interface {
	PublishSourceBuildRootfs(context.Context, SourceBuildRootfsInput) (SourceBuildRootfs, error)
	// Historical selected conversion; current approval/scans/native fences are separate.
	GetCurrentSourceBuildRootfs(context.Context, string, string, string) (SourceBuildRootfs, error)
}

func prepareSourceBuildRootfs(in SourceBuildRootfsInput) (SourceBuildRootfsInput, string, error) {
	for _, id := range []string{in.ID, in.PublicationID, in.AccountID, in.AppID, in.DeploymentID, in.BaseProducerID} {
		if !validStandardResourceRead(id, id) {
			return in, "", ErrInvalidArgument
		}
	}
	if in.OrgID != "" && !validStandardResourceRead(in.OrgID, in.OrgID) || api.ValidateScope(in.Scope) != nil ||
		!validSourceHash(in.PublicationHash) || !validSourceHash(in.BaseInputHash) || !validSourceHash(in.IntentHash) ||
		in.LayoutVersion != SourceBuildRootfsLayout || in.StorageKey == "" || in.RootfsPath == "" || in.ContentBytes < 0 || in.ArtifactBytes <= 0 ||
		ociref.ValidateDigest(in.ArtifactDigest) != nil || ociref.ValidateDigest(in.GuestInitDigest) != nil {
		return in, "", ErrInvalidArgument
	}
	if err := checkSourceBuildRootfsKind(in); err != nil {
		return in, "", err
	}
	in.ID, in.PublicationID, in.AccountID, in.AppID, in.DeploymentID, in.BaseProducerID = canonicalStandardUUID(in.ID), canonicalStandardUUID(in.PublicationID), canonicalStandardUUID(in.AccountID), canonicalStandardUUID(in.AppID), canonicalStandardUUID(in.DeploymentID), canonicalStandardUUID(in.BaseProducerID)
	in.OrgID = registryCanonicalOrg(in.OrgID)
	hash, err := standardReviewDigest(in)
	return in, hash, err
}

func checkSourceBuildRootfsKind(in SourceBuildRootfsInput) error {
	switch in.Kind {
	case "source-app-layer":
		if in.RunnerDigest != "" || in.Runtime != "" && !SharedSourceAppRuntime(in.Runtime) {
			return ErrInvalidArgument
		}
	case "function-layer":
		if in.Runtime == "" || len(in.Runtime) > api.ApplicationStandardMaxResourceNameBytes || ociref.ValidateDigest(in.RunnerDigest) != nil {
			return ErrInvalidArgument
		}
	default:
		return ErrInvalidArgument
	}
	return nil
}

func validSourceHash(hash string) bool {
	return len(hash) == 64 && ociref.ValidateDigest("sha256:"+hash) == nil
}

func validateSourceBuildRootfs(value SourceBuildRootfs) error {
	in, hash, err := prepareSourceBuildRootfs(value.Input)
	if err != nil {
		return err
	}
	if in.ID != value.ID || hash != value.InputHash || value.PublishedAt.IsZero() || !value.ExpiresAt.After(value.PublishedAt) || value.ExpiresAt.Sub(value.PublishedAt) > api.BuildExportPublicationVerificationTTL {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func checkSourceBuildRootfsParent(in SourceBuildRootfsInput, parent BuildExportPublication, app App, dep Deployment, now time.Time) error {
	return checkSourceBuildRootfsOwner(in, parent, sourceBuildRootfsOwnerIntent(app, dep), dep.Status, now)
}

func checkSourceBuildRootfsOwner(in SourceBuildRootfsInput, parent BuildExportPublication, intent sourceBuildRootfsIntent, status DeploymentStatus, now time.Time) error {
	if err := validateBuildExportPublication(parent); err != nil {
		return err
	}
	c := parent.Input.Claims
	if in.PublicationID != parent.ID || in.PublicationHash != parent.InputHash || in.AccountID != c.AccountID || in.OrgID != c.OrgID ||
		in.AppID != c.AppID || in.DeploymentID != c.DeploymentID || in.Scope != intent.Scope || parent.VerifiedAt.After(now) || !parent.ExpiresAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	if status != DeployPending && status != DeployBuilding && status != DeployImaging && status != DeploySnapshotting {
		return ErrApplicationStandardRuntimeStale
	}
	kind, runtime := SourceBuildRootfsKindForClaim(App{Type: intent.Type, Runtime: intent.Runtime}, Deployment{Handler: intent.Handler}, c.Runtime)
	intentHash, err := hashSourceBuildRootfsIntent(intent)
	if err != nil {
		return err
	}
	if in.Kind != kind || in.Runtime != runtime || in.IntentHash != intentHash {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

// sharedSourceAppRuntimes are the runner bases a source-built container app
// may run on instead of the minimal base, holding only /app in its layer
// (docs/adr/1500-shared-runtime-source-apps.md, H10-140).
var sharedSourceAppRuntimes = map[string]bool{"node22": true, "node24": true}

// SharedSourceAppRuntime reports whether runtime is a runner base a
// source-built container app may claim.
func SharedSourceAppRuntime(runtime string) bool { return sharedSourceAppRuntimes[runtime] }

// SharedSourceAppCandidate reports whether a source build of app may run on a
// shared runner base at all: a container app without its own runtime. Job-mode
// boots still assume the minimal base, so jobs never qualify.
func SharedSourceAppCandidate(app App) bool {
	return app.Type != AppTypeFunction && app.Runtime == "" && app.Manifest.ExecutionMode != api.ExecutionModeJob
}

// SourceRuntimeClaimAllowed reports whether a signed build export may claim
// runtime for app: the app's own runtime, or a shared runner base for a
// container app that declares none.
func SourceRuntimeClaimAllowed(app App, claimed string) bool {
	return claimed == app.Runtime || app.Type != AppTypeFunction && app.Runtime == "" && SharedSourceAppRuntime(claimed)
}

// SourceBuildRootfsKindForClaim is SourceBuildRootfsKind for a build whose
// signed export claims claimedRuntime: a container app's layer then records
// the shared runner base it was built for.
func SourceBuildRootfsKindForClaim(app App, dep Deployment, claimedRuntime string) (string, string) {
	kind, runtime := SourceBuildRootfsKind(app, dep)
	if kind == "source-app-layer" && claimedRuntime != app.Runtime && SourceRuntimeClaimAllowed(app, claimedRuntime) {
		return kind, claimedRuntime
	}
	return kind, runtime
}

func SourceBuildRootfsKind(app App, dep Deployment) (string, string) {
	if app.Type == AppTypeFunction {
		runtime := app.Runtime
		if runtime == "" {
			runtime = dep.Handler
		}
		return "function-layer", runtime
	}
	return "source-app-layer", ""
}

func checkSourceBuildRootfsBase(in SourceBuildRootfsInput, base BaseImageProducer) error {
	if err := validateBaseImageProducer(base); err != nil {
		return err
	}
	if in.BaseProducerID != base.ID || in.BaseInputHash != base.InputHash || base.Input.LayoutVersion != imagechain.BaseLayoutVersion ||
		base.Input.Artifact.StorageKey != RuntimeBaseKeyForArch(in.Runtime, "amd64") || in.StorageKey == base.Input.Artifact.StorageKey ||
		in.GuestInitDigest != base.Input.GuestInitDigest {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func sourceBuildRootfsMetadataMatches(value SourceBuildRootfs, dep Deployment) bool {
	in := value.Input
	return in.Scope == dep.Scope && in.RootfsPath == dep.RootfsPath && in.StorageKey == dep.RootfsKey && in.ContentBytes == dep.RootfsBytes &&
		dep.RuntimeBase == sourceBuildRootfsRuntimeBase(in)
}

// sourceBuildRootfsRuntimeBase is the deployment runtime_base a source rootfs
// stamps: its shared runner base, or "" for function layers and app layers on
// the app's own base.
func sourceBuildRootfsRuntimeBase(in SourceBuildRootfsInput) string {
	if in.Kind == "source-app-layer" {
		return in.Runtime
	}
	return ""
}

// SetDeploymentRuntimeBase records the shared runner base for an in-flight
// deployment whose source build has no signed export (H10-140).
func (s *PgStore) SetDeploymentRuntimeBase(ctx context.Context, id, runtime string) error {
	if runtime != "" && !SharedSourceAppRuntime(runtime) {
		return ErrInvalidArgument
	}
	return mapErr(sqlc.New().StampDeploymentRuntimeBase(ctx, s.pool, sqlc.StampDeploymentRuntimeBaseParams{DeploymentID: mustPgUUID(id), RuntimeBase: runtime}))
}

// SetDeploymentRuntimeBase is the MemStore twin of the PgStore method.
func (m *MemStore) SetDeploymentRuntimeBase(_ context.Context, id, runtime string) error {
	if runtime != "" && !SharedSourceAppRuntime(runtime) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	switch d.Status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting:
	default:
		return nil
	}
	d.RuntimeBase = runtime
	m.putDeploymentLocked(id, d)
	return nil
}
