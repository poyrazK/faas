package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// prepareFunctionRuntimeRelease never infers provenance for an older build.
// New builds record the host-resolved ref before imaged consumes their OCI export.
func (h *Handler) prepareFunctionRuntimeRelease(ctx context.Context, app state.App, dep state.Deployment, runtime, key string) (*state.RuntimeRelease, error) {
	releases, ok := h.store.(state.RuntimeReleaseStore)
	if !ok {
		return nil, errors.New("runtime release store is unavailable")
	}
	target, err := h.explicitRuntimeUpgradeTarget(ctx, app, dep, runtime)
	if err != nil {
		return nil, err
	}
	build, err := h.store.BuildByDeployment(ctx, dep.ID)
	if errors.Is(err, state.ErrNotFound) && target == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ref, err := releases.BuildRuntimeBaseRef(ctx, build.ID)
	if errors.Is(err, state.ErrNotFound) && target == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ref == "" {
		if target != nil {
			return nil, errors.New("runtime update build has no recorded runtime base")
		}
		return nil, nil
	}
	if target != nil && target.SourceRef != ref {
		return nil, errors.New("runtime update build used a different runtime source")
	}
	if target != nil {
		provenance, err := h.store.BuildProvenanceByBuildID(ctx, build.ID)
		if err != nil {
			return nil, fmt.Errorf("read runtime update build provenance: %w", err)
		}
		if provenance.SourceSHA256 != dep.SourceSHA256 {
			return nil, errors.New("runtime update build used different source bytes")
		}
	}
	// Retries retain an already bound generation, even after a daemon update.
	bound, err := releases.RuntimeReleaseForArtifact(ctx, app.AccountID, key)
	if err == nil {
		if bound.SourceRef != ref || bound.Runtime != runtime || bound.Architecture != goruntime.GOARCH || (target != nil && bound.ID != target.ID) {
			return nil, state.ErrConflict
		}
		if err := h.verifyRuntimeRelease(ctx, bound); err != nil {
			return nil, err
		}
		if target != nil {
			if err := h.verifyRuntimeUpgradeScan(ctx, bound); err != nil {
				return nil, err
			}
		}
		return &bound, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return nil, err
	}
	if target != nil {
		// Reuse the selected ext4 bytes, including its exact guest-init and
		// layout. The currently installed guest-init cannot select a new ID.
		if err := h.verifyRuntimeRelease(ctx, *target); err != nil {
			return nil, err
		}
		if err := h.verifyRuntimeUpgradeScan(ctx, *target); err != nil {
			return nil, err
		}
		return target, nil
	}
	release, err := h.ensureRuntimeRelease(ctx, releases, runtime, goruntime.GOARCH, ref)
	if err != nil {
		return nil, err
	}
	return &release, nil
}

func (h *Handler) explicitRuntimeUpgradeTarget(ctx context.Context, app state.App, dep state.Deployment, runtime string) (*state.RuntimeRelease, error) {
	if err := state.CheckDeploymentRuntimeUpgradeBaseline(ctx, h.store, dep.ID); err != nil {
		return nil, err
	}
	targets, ok := h.store.(state.RuntimeUpgradeTargetStore)
	if !ok {
		return nil, nil
	}
	target, err := targets.DeploymentRuntimeUpgradeTarget(ctx, dep.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read runtime update target: %w", err)
	}
	if target.Validate() != nil || app.ID != dep.AppID || app.Type != state.AppTypeFunction ||
		target.Runtime != runtime || target.Runtime != app.Runtime || target.Architecture != goruntime.GOARCH || dep.SourceSHA256 == "" {
		return nil, fmt.Errorf("%w: incompatible runtime update image target", state.ErrConflict)
	}
	if err := state.RequireRuntimeReleaseQualification(ctx, h.store, target); err != nil {
		return nil, err
	}
	return &target, nil
}

// A historical scan is not native qualification. Require complete scan
// evidence before preparing an update; vmmd still owns its admission policy.
func (h *Handler) verifyRuntimeUpgradeScan(ctx context.Context, target state.RuntimeRelease) error {
	be, err := h.storageFor()
	if err != nil {
		return err
	}
	r, err := be.Get(ctx, wire.ScanKeyForBaseKey(target.BaseKey()))
	if err != nil {
		return fmt.Errorf("read runtime update scan evidence: %w", err)
	}
	defer func() { _ = r.Close() }()
	raw, err := io.ReadAll(io.LimitReader(r, api.RuntimeReleaseSidecarMaxBytes+1))
	if err != nil || len(raw) > api.RuntimeReleaseSidecarMaxBytes {
		return errors.New("runtime update scan evidence is unreadable or oversized")
	}
	var scan struct {
		Image                string         `json:"image"`
		Findings             map[string]int `json:"findings"`
		FixAvailableFindings map[string]int `json:"fix_available_findings"`
		ScannedAt            time.Time      `json:"scanned_at"`
	}
	if json.Unmarshal(raw, &scan) != nil || scan.Image != target.SourceRef || scan.Findings == nil || scan.FixAvailableFindings == nil || scan.ScannedAt.IsZero() || scan.Findings[SeverityCritical] >= 9999 {
		return errors.New("runtime update target lacks usable scan evidence")
	}
	return nil
}

func (h *Handler) ensureDeploymentRuntimeBaseForDeployment(ctx context.Context, app state.App, dep state.Deployment) error {
	target, err := h.explicitRuntimeUpgradeTarget(ctx, app, dep, app.Runtime)
	if err != nil {
		return err
	}
	if target == nil {
		return h.ensureDeploymentRuntimeBase(ctx, app)
	}
	if !h.runtimeBaseStagingEnabled {
		return errors.New("runtime update requires immutable base staging")
	}
	current, err := h.store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		return err
	}
	releases, ok := h.store.(state.RuntimeReleaseStore)
	if !ok {
		return errors.New("runtime update binding store is unavailable")
	}
	bound, err := releases.RuntimeReleaseForArtifact(ctx, app.AccountID, current.RootfsKey)
	if err != nil {
		return fmt.Errorf("read runtime update artifact binding: %w", err)
	}
	if bound.ID != target.ID {
		return fmt.Errorf("%w: runtime update artifact does not use its selected target", state.ErrConflict)
	}
	// Function assembly already verified and replicated the exact base. Do
	// not stage a mutable daemon default as a second source of authority.
	return nil
}

func (h *Handler) verifyRuntimeRelease(ctx context.Context, r state.RuntimeRelease) error {
	if err := r.Validate(); err != nil {
		return err
	}
	be, err := h.storageFor()
	if err != nil {
		return err
	}
	content, err := h.publishedBytesIdentity(ctx, be, r.BaseKey())
	if err != nil {
		return err
	}
	if content.SHA256 != "sha256:"+r.BaseSHA256 {
		return errors.New("published runtime release bytes do not match recorded identity")
	}
	return h.validateExistingBaseArtifact(ctx, be, r.BaseKey())
}

func (h *Handler) ensureRuntimeRelease(ctx context.Context, releases state.RuntimeReleaseStore, runtime, arch, ref string) (state.RuntimeRelease, error) {
	guest, err := guestInitBinaryDigest(h.guestInitPath)
	if err != nil {
		return state.RuntimeRelease{}, err
	}
	candidate := state.RuntimeRelease{Runtime: runtime, Architecture: arch, SourceRef: ref, GuestInitSHA256: strings.TrimPrefix(guest, "sha256:"), LayoutVersion: baseLayoutVersion, BaseSHA256: strings.Repeat("0", 64)}
	candidate.ID = candidate.Identity()
	if err := candidate.Validate(); err != nil {
		return state.RuntimeRelease{}, fmt.Errorf("immutable runtime input: %w", err)
	}
	h.runtimeBaseMu.Lock()
	defer h.runtimeBaseMu.Unlock()
	existing, err := releases.FindRuntimeRelease(ctx, candidate)
	if err == nil {
		return existing, h.verifyRuntimeRelease(ctx, existing)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.RuntimeRelease{}, err
	}
	be, err := h.storageFor()
	if err != nil {
		return state.RuntimeRelease{}, err
	}
	staging := "base/release-staging/runner-" + runtime + "-" + arch + "-" + uuid.NewString() + ".ext4"
	digest := staging + ".digest"
	defer cleanupRuntimeStaging(ctx, be, staging, digest)
	// A fresh private key cannot fall back to an unrelated installed base.
	// Applying the full OCI chain preserves drive0/drive1 without mounting a
	// mutable parent whose generation may have changed since the source build.
	_, err = h.ensureBaseExt4(ctx, ref, staging, digest, "", "", "")
	if err != nil {
		return state.RuntimeRelease{}, err
	}
	sidecar, err := be.Get(ctx, digest)
	if err != nil {
		return state.RuntimeRelease{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(sidecar, api.RuntimeReleaseSidecarMaxBytes))
	_ = sidecar.Close()
	_, actualRef, current := parseBaseDigestSidecar(string(body), guest)
	if readErr != nil || !current || actualRef != ref {
		return state.RuntimeRelease{}, errors.New("staged runtime lacks exact source and guest-init evidence")
	}
	content, err := h.publishedBytesIdentity(ctx, be, staging)
	if err != nil {
		return state.RuntimeRelease{}, err
	}
	candidate.BaseSHA256 = strings.TrimPrefix(content.SHA256, "sha256:")
	candidate.ID = candidate.Identity()
	if err := copyRuntimeObject(ctx, be, staging, candidate.BaseKey()); err != nil {
		return state.RuntimeRelease{}, err
	}
	if err := putBaseContent(ctx, be, candidate.BaseKey(), content); err != nil {
		return state.RuntimeRelease{}, err
	}
	if err := copyRuntimeObject(ctx, be, wire.ScanKeyForBaseKey(staging), wire.ScanKeyForBaseKey(candidate.BaseKey())); err != nil {
		return state.RuntimeRelease{}, err
	}
	if err := h.verifyRuntimeRelease(ctx, candidate); err != nil {
		return state.RuntimeRelease{}, err
	}
	// Concurrent nodes may build different ext4 bytes from identical inputs.
	// The first publication wins; every artifact uses that canonical generation.
	canonical, err := releases.PublishRuntimeRelease(ctx, candidate)
	if err != nil {
		return state.RuntimeRelease{}, err
	}
	return canonical, h.verifyRuntimeRelease(ctx, canonical)
}

func copyRuntimeObject(ctx context.Context, be storage.StorageBackend, from, to string) error {
	r, err := be.Get(ctx, from)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return be.Put(ctx, to, r)
}

func (h *Handler) replicateRuntimeRelease(ctx context.Context, release state.RuntimeRelease) error {
	if h.replicator == nil {
		return nil
	}
	replicator, ok := h.replicator.(RuntimeReleaseReplicator)
	if !ok {
		return errors.New("split-box artifact replicator does not support immutable runtime releases")
	}
	if err := replicator.ReplicateRuntimeRelease(ctx, release.BaseKey()); err != nil {
		return fmt.Errorf("imaged: replicate runtime base: %w", err)
	}
	return nil
}

func cleanupRuntimeStaging(ctx context.Context, be storage.StorageBackend, key, digest string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, k := range []string{key, digest, baseContentKey(key), wire.ScanKeyForBaseKey(key)} {
		_ = be.Delete(cleanup, k)
	}
}
