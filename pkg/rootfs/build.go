package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// Builder turns the OCI layers that sit above a base image into a bootable per-app
// ext4 layer (drive1, spec §4.6): apply layers into a staging tree, inject
// guest-init as /sbin/init and the app.json contract, enforce the plan cap, then
// mkfs from the staging directory. Everything except the final unprivileged mkfs
// runs in pure Go and is unit-tested.
//
// The produced ext4 is published through a StorageBackend (issue #96, ADR-025
// axis 2). Two ways are supported:
//
//   - Storage + StorageKey set: the builder mkfs-es into a tmp file, then Put's
//     the bytes into storage under StorageKey. The tmp file is removed before
//     returning. This is the production path (the storage backend resolves to
//     /srv/fc + /var/lib/faas/apps via LocalStorageBackend + PrefixRouter).
//   - OutImage set (legacy): the builder mkfs-es directly into OutImage. Kept
//     for the integration test (build_integration_test.go) which exercises a
//     real mkfs against an on-disk tmpfile; the production imaged path never
//     reaches here.
//
// Exactly one of {Storage, OutImage} must be set. The Builder refuses to
// silently pick a default — the legacy path's silent fallback would hide the
// production wiring (a misconfigured cmd/imaged would write into CWD).

// Runner executes the mkfs command. Injected so Build is testable without
// e2fsprogs; ExecRunner-style impls live alongside vmmd's on the real host.
type Runner interface {
	Run(ctx context.Context, argv []string) error
}

// Signer signs a published artifact. Wired via WithSigner (nil-safe;
// nil = no signing — unit tests + the legacy OutImage path). The
// production wiring is *cosign.LocalSigner from cmd/imaged; ADR-038
// §Decision names this as the platform's build-attestation seam.
//
// Sign is called once per publishExt4 / publishBaseExt4 after the
// Storage.Put succeeds. The signer owns the storage backend and the
// sig-key derivation — pkg/rootfs only supplies the canonical
// layerKey + sigKey pair.
//
// Per-key contract: the signer MUST be safe to call from many
// goroutines (imaged builds layers concurrently per spec §4.6
// per-app layer write path).
type Signer interface {
	Sign(ctx context.Context, layerKey, sigKey string) error
}

// Builder assembles app layers.
type Builder struct {
	run Runner
	// signer is optional; nil = no signing (legacy / tests).
	// Wired via WithSigner at cmd/imaged startup; never nil-checked
	// inline (signer == nil short-circuits in publishExt4 +
	// publishBaseExt4).
	signer Signer
}

// NewBuilder wires a Builder with the command runner used for mkfs.
func NewBuilder(run Runner) *Builder {
	return &Builder{run: run}
}

// WithSigner attaches the build-attestation signer. Called from
// cmd/imaged after loading /etc/faas/secrets/sign.key (fail-loud
// at startup per ADR-038 §Consequences). Passing nil clears the
// signer (unit tests + the legacy OutImage path that never
// publishes to Storage).
func (b *Builder) WithSigner(s Signer) *Builder {
	b.signer = s
	return b
}

// BuildInput is one app-layer build.
type BuildInput struct {
	// Layers are the above-base OCI layers for main, or all OCI layers for
	// an independent sidecar, bottom-to-top and gzip-compressed.
	Layers []io.Reader
	// Manifest is the /etc/faas/app.json contract to inject.
	Manifest api.AppManifest
	// WorkloadName and WorkloadManifest optionally add a workload-specific
	// contract under /etc/faas/workloads/<name>/workload.json. This is used
	// for sidecar images: their effective OCI entrypoint and image-default
	// environment travel with the immutable sidecar layer; deployment overrides
	// remain sealed until vmmd opens them for an instance.
	WorkloadName     string
	WorkloadManifest *api.AppManifest
	// GuestInitPath is the guest-init binary injected as /sbin/init.
	GuestInitPath string
	// Plan sets the app-layer cap.
	Plan api.Plan
	// Storage is the artifact backend the produced ext4 is Put into.
	// Required when StorageKey is set; mutually exclusive with OutImage.
	Storage storage.StorageBackend
	// StorageKey is the key the produced ext4 is published under, e.g.
	// "apps/<slug>/<deploymentID>.ext4". Required when Storage is set;
	// mutually exclusive with OutImage.
	StorageKey string
	// OutImage is the legacy on-disk target. The integration test
	// (TestBuildRealMkfs) uses it; production wiring uses
	// Storage + StorageKey. Kept for one release per the ADR-025
	// deprecation window.
	OutImage string
	// TarballPath, when set, is the customer's source tarball applied
	// into /app during layer assembly. Used by the function-deploy path
	// (spec §4.9, M7). Empty skips the tarball application step.
	TarballPath string
	// FunctionHandlerPath, when set, is the handler path named by the
	// injected function manifest (for example /app/node22.js). Function
	// source archives conventionally contain handler.js at their project
	// root; the builder creates the runtime-specific alias when needed.
	// Empty preserves the plain tarball/image behavior.
	FunctionHandlerPath string
	// FunctionHandlerSourcePath, when set, names an already-materialized
	// handler inside the applied OCI layers. It is copied to
	// FunctionHandlerPath before the runtime-specific adapter is applied.
	// This is the source-build path: builderd's OCI export already contains
	// the dependency tree, so imaged must not unpack the original source
	// tarball a second time and discard that build output.
	FunctionHandlerSourcePath string
	// FunctionRunnerPath, when set, is copied into the layer at
	// /usr/local/bin/faas-runner so the guest can exec it. Wired from
	// cmd/imaged's config; empty skips the runner injection.
	FunctionRunnerPath string
	// SBOMRun, when set, runs after publishExt4 against the staging
	// directory (still alive — the cleanup defer has not fired yet)
	// to produce a CycloneDX SBOM (issue #299 / ADR-038 Phase 3).
	// The returned bytes are validated for JSON-shape and Put under
	// SBOMStorageKey. Nil = no SBOM emission (legacy / unit tests).
	// Production wires the syft subprocess via imaged's WithSyftRun
	// seam; tests inject a stub returning canned CycloneDX bytes.
	//
	// Best-effort: a syft error or non-JSON output leaves SBOMKey
	// empty and the build still succeeds. The build_provenance row
	// builderd writes immediately after carries an empty
	// sbom_storage_key in that case (observability metadata only;
	// schema §4.2 lets the deployment succeed without a
	// provenance row).
	SBOMRun func(ctx context.Context, dir string) ([]byte, error)
	// SBOMStorageKey is the storage key the SBOM is Put under. When
	// SBOMRun is non-nil, SBOMKey MUST be set (validated by
	// validateOutputTarget's sibling check). When SBOMRun is nil,
	// SBOMStorageKey is ignored.
	SBOMStorageKey string
	// FullRootfs, when true, routes the build through BuildFullRootfs
	// instead of the two-drive path (ADR-141 §Decision 1). The
	// caller supplies the deployment's `FullRootfsOverride` /
	// `FullRootfsAllowAuto` decision (state.Deployment widens in
	// commit 6); commit 5 lays the flag wiring only. Default
	// false preserves today's two-drive behaviour for every
	// existing caller.
	FullRootfs bool
	// CommandPATH is consulted only by the full-rootfs launch check. See
	// BuildFullRootfsInput.CommandPATH; shared-base layering is unchanged.
	CommandPATH *string
}

// BuildResult reports the produced layer.
type BuildResult struct {
	// ImageKey is the storage key the ext4 was published under, when
	// Storage was used. Empty when the legacy OutImage path produced
	// the file.
	ImageKey string
	// ImagePath is the on-disk path the ext4 was written to, when
	// OutImage was used. Empty when Storage published the file.
	ImagePath      string
	SizeMB         int
	ContentBytes   int64
	ArtifactDigest string
	ArtifactBytes  int64
	// SBOMKey is the storage key the CycloneDX SBOM was published
	// under (issue #299 / ADR-038 Phase 3). Empty when the build
	// did not configure SBOMRun + SBOMStorageKey, or when the
	// emission failed (best-effort: the build still succeeds).
	SBOMKey string
	// RunnerDigest is the canonical sha256 digest of the exact function
	// runner bytes copied to /usr/local/bin/faas-runner. Empty when no
	// function runner was injected.
	RunnerDigest string
	// GuestInitDigest covers the exact bytes injected into this app layer.
	GuestInitDigest string
}

// Build runs the pipeline. It stages into a temp dir that is always removed.
func (b *Builder) Build(ctx context.Context, in BuildInput) (BuildResult, error) {
	// ADR-141 §Decision 1: when FullRootfs is true, Build forwards
	// to BuildFullRootfs. The two paths share Validate() +
	// validateOutputTarget + the InjectManifest / InjectGuestInit /
	// mkfs helpers; the only divergence is the layer-application
	// loop (full-rootfs applies ALL layers, no LayersAboveBase) and
	// the absence of the /upper staging step (full-rootfs images
	// become drive0+vda in the guest, not drive1).
	if in.FullRootfs {
		return b.BuildFullRootfs(ctx, BuildFullRootfsInput{
			Layers:              in.Layers,
			Manifest:            in.Manifest,
			GuestInitPath:       in.GuestInitPath,
			Plan:                in.Plan,
			Storage:             in.Storage,
			StorageKey:          in.StorageKey,
			OutImage:            in.OutImage,
			TarballPath:         in.TarballPath,
			FunctionHandlerPath: in.FunctionHandlerPath,
			FunctionRunnerPath:  in.FunctionRunnerPath,
			SBOMRun:             in.SBOMRun,
			SBOMStorageKey:      in.SBOMStorageKey,
			CommandPATH:         in.CommandPATH,
		})
	}
	limits, ok := limitsFor(in.Plan)
	if !ok {
		return BuildResult{}, fmt.Errorf("rootfs: unknown plan %q", in.Plan)
	}
	if err := in.Manifest.Validate(); err != nil {
		return BuildResult{}, err
	}
	if err := validateOutputTarget(in); err != nil {
		return BuildResult{}, err
	}

	staging, err := os.MkdirTemp("", "faas-layer-*")
	if err != nil {
		return BuildResult{}, fmt.Errorf("rootfs: staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	budget := newExtractionBudget(limits.LayerExtractionBudgetBytes())
	opaqueRoot := false
	for i, layer := range in.Layers {
		// The main workload overlays the shared base: its artifact becomes
		// overlayfs' upper directory after stageAppUpper, so OCI whiteouts stay
		// overlayfs markers that can hide paths supplied by the base drive. A
		// sidecar consumes all OCI layers as an independent root. Both filter
		// image entries below /dev, /proc, /sys, and /tmp, which the shared base
		// owns, before they can become mounted staging paths.
		if err := applyLayerGzForBuild(staging, layer, in.WorkloadManifest != nil, &opaqueRoot, budget); err != nil {
			return BuildResult{}, layerApplyError(limits, i, err)
		}
	}

	// A customer image must not be able to smuggle the full-rootfs marker
	// into the optimized two-drive artifact and make guest-init bypass the
	// shared base. Only BuildFullRootfs writes the marker after all layers
	// have been applied.
	if err := removeFullRootfsMarker(staging); err != nil {
		return BuildResult{}, err
	}
	// Function-deploy path (spec §4.9, M7). When TarballPath is set the
	// customer's source tarball is unpacked at /app; when
	// FunctionRunnerPath is set the runner shim is injected at
	// /usr/local/bin/faas-runner. Both default to no-op for the plain
	// image path so existing callers don't change.
	if in.TarballPath != "" {
		// Issue #197 B3.7: cap the function tarball's cumulative unpacked
		// size at the plan's app-layer cap. The apid-side upload cap
		// (SourceTarballMaxMB) guards the on-disk tarball size; this cap
		// guards the post-unpack size so a small gzipped tarball can't
		// expand past the plan limit.
		capBytes := int64(limits.AppLayerMaxMB) * 1024 * 1024
		if err := ApplyTarball(staging, in.TarballPath, capBytes); err != nil {
			// Promote the sentinel to the plan-scoped RFC 7807 Problem
			// so the customer-facing deploy failure carries the limit +
			// observed value + docs URL instead of a generic "function
			// tarball exceeds cap" string. The post-unpack observed size
			// is `Written + Entry` — the total that would have been
			// written had we not aborted (matches the message at the
			// ErrTarballExceedsCap site). Mirrors CheckCap's
			// api.ErrAppLayerTooLarge return at size.go:74 so callers
			// see the same Problem shape for both cap violations.
			var capErr *ErrTarballExceedsCap
			if errors.As(err, &capErr) {
				return BuildResult{}, api.ErrAppLayerTooLarge(limits, capErr.WrittenBytes+capErr.EntryBytes)
			}
			return BuildResult{}, err
		}
		if in.FunctionHandlerPath != "" {
			if err := NormalizeFunctionHandler(staging, in.FunctionHandlerPath); err != nil {
				return BuildResult{}, err
			}
		}
	}
	if in.FunctionHandlerSourcePath != "" {
		if in.FunctionHandlerPath == "" {
			return BuildResult{}, errors.New("rootfs: function handler source requires a target path")
		}
		if err := NormalizeFunctionHandlerFrom(staging, in.FunctionHandlerSourcePath, in.FunctionHandlerPath); err != nil {
			return BuildResult{}, err
		}
	}
	runnerDigest := ""
	if in.FunctionRunnerPath != "" {
		var err error
		runnerDigest, err = injectFunctionRunner(staging, in.FunctionRunnerPath)
		if err != nil {
			return BuildResult{}, err
		}
	}
	guestInitDigest, err := injectGuestInitWithDigest(staging, in.GuestInitPath)
	if err != nil {
		return BuildResult{}, err
	}
	if err := InjectManifest(staging, in.Manifest); err != nil {
		return BuildResult{}, err
	}
	// The optimized two-drive path still has to carry the image's user
	// database into the guest. guest-init resolves a named OCI USER from this
	// bounded table after the overlay has been assembled. Without it, names
	// such as distroless' "nonroot" silently fall back to uid 1000 even when
	// the image declares uid 65532, which can make its 0700 working directory
	// inaccessible before exec.
	passwdEntries, err := parseStagingPasswd(staging)
	if err != nil {
		return BuildResult{}, fmt.Errorf("rootfs: parse optimized /etc/passwd: %w", err)
	}
	if passwdEntries == nil {
		passwdEntries = make(map[string]PasswdEntry)
	}
	if err := writePasswdTable(staging, passwdEntries, api.UserUIDOverrideMax[in.Plan]); err != nil {
		return BuildResult{}, fmt.Errorf("rootfs: build optimized passwd table: %w", err)
	}
	if in.WorkloadManifest != nil {
		if in.WorkloadName == "" {
			return BuildResult{}, errors.New("rootfs: workload manifest requires a workload name")
		}
		if err := in.WorkloadManifest.ValidatePlan(in.Plan); err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: workload manifest: %w", err)
		}
		if err := InjectWorkloadManifest(staging, in.WorkloadName, *in.WorkloadManifest); err != nil {
			return BuildResult{}, err
		}
		// Sidecar images are mounted as independent roots when their main
		// deployment uses full-rootfs. Keep the standard container mount
		// points in the immutable sidecar tree so guest-init can attach
		// /proc, /sys, /dev, and /tmp after chrooting the workload.
		if err := ensureWorkloadMountpoints(staging); err != nil {
			return BuildResult{}, err
		}
	}

	stats, err := InspectStaging(staging)
	if err != nil {
		return BuildResult{}, err
	}
	if _, err := CheckCapForStaging(limits, stats); err != nil {
		return BuildResult{}, err // *api.Problem naming cap + observed size
	}
	// The public ephemeral_disk_max_mb contract is the logical capacity of
	// drive1, including immutable app content and ext4 metadata. Build the
	// filesystem at that stable plan capacity so a fresh guest has useful
	// writable headroom. Keeping the size in the deployment artifact also
	// avoids resize2fs work on every cold or snapshot wake.
	sizeMB := limits.EphemeralDiskMaxMB()

	// Issue #299 / ADR-038 Phase 3: SBOM emission runs on the staging
	// dir BEFORE the drive1 wrapper is added and the cleanup defer fires (the staging dir is the only
	// artefact that contains the customer's source tree, and the user
	// wants the SBOM to enumerate the actual files present in the
	// produced layer). emitSBOM is best-effort: a syft error or
	// non-JSON output leaves SBOMKey empty and the build still
	// succeeds. The build_provenance row builderd writes immediately
	// after carries an empty sbom_storage_key in that case (schema
	// §4.2 lets the deployment succeed without a provenance row).
	sbomKey, sbomErr := b.emitSBOM(ctx, in, staging)
	if sbomErr != nil {
		// Mirror the storage-write Warn pattern below: best-effort,
		// never fails the build. We do not surface the error to
		// callers because the apid endpoint apid/handlers_ext.go's
		// getBuildSbom will return 503 build_sbom_unavailable
		// instead — the operator sees the missing SBOM and re-runs
		// imaged to populate the column for future builds.
		_ = sbomErr
	}
	// drive1 is mounted at /overlay by guest-init and its /upper directory
	// is used as overlayfs' writable layer. The ext4 artifact therefore must
	// carry the assembled app tree under /upper; Linux cannot see drive1's
	// files until guest-init mounts it, so a root-level app.json would be
	// invisible after the overlay is assembled.
	if err := stageAppUpper(staging, opaqueRoot); err != nil {
		return BuildResult{}, err
	}

	sizeMB, identity, err := b.publishExt4(ctx, in, staging, sizeMB, limits)
	if err != nil {
		return BuildResult{}, err
	}

	res := BuildResult{
		SizeMB:          sizeMB,
		ContentBytes:    stats.ContentBytes,
		ArtifactDigest:  identity.Digest,
		ArtifactBytes:   identity.Bytes,
		SBOMKey:         sbomKey,
		RunnerDigest:    runnerDigest,
		GuestInitDigest: guestInitDigest,
	}
	if in.OutImage != "" {
		res.ImagePath = in.OutImage
	} else {
		res.ImageKey = in.StorageKey
	}
	return res, nil
}

// stageAppUpper moves the completed per-app tree below the /upper directory
// consumed by guest-init's overlay assembly. It preserves a customer image's
// own top-level "upper" path by treating it as normal app content at
// /upper/upper inside the artifact.
func stageAppUpper(staging string, opaqueRoots ...bool) error {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return fmt.Errorf("rootfs: read app staging: %w", err)
	}
	upper, err := os.MkdirTemp(staging, ".faas-upper-wrapper-")
	if err != nil {
		return fmt.Errorf("rootfs: create app upper wrapper: %w", err)
	}
	if err := chmodImageDirectory(upper, 0o755); err != nil {
		return fmt.Errorf("rootfs: set app upper wrapper mode: %w", err)
	}
	if len(opaqueRoots) > 0 && opaqueRoots[0] {
		// Set metadata while the wrapper is empty; clearing it later would
		// discard the application content we are about to move.
		if err := applyOverlayOpaque(upper); err != nil {
			return fmt.Errorf("rootfs: preserve root opacity: %w", err)
		}
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(staging, entry.Name()), filepath.Join(upper, entry.Name())); err != nil {
			return fmt.Errorf("rootfs: move app entry into upper: %w", err)
		}
	}
	return os.Rename(upper, filepath.Join(staging, "upper"))
}

// ensureWorkloadMountpoints creates the mount targets needed by a sidecar
// that runs from its own root. The directories are created before stageAppUpper
// moves the assembled image below /upper, so the sidecar ext4 remains
// read-only while guest-init attaches the shared guest pseudo-filesystems.
func ensureWorkloadMountpoints(staging string) error {
	for _, rel := range []string{"dev", "proc", "sys", "tmp"} {
		if err := ensureRuntimeDirectory(staging, rel); err != nil {
			return fmt.Errorf("rootfs: sidecar mountpoint %q: %w", rel, err)
		}
	}
	return nil
}

// ensureRuntimeDirectory creates a path beneath staging while resolving any
// existing ancestor symlinks with the same containment rules as OCI layer
// extraction. The final target must resolve to a directory; a customer image
// cannot turn a platform mountpoint into a regular file or an escape path.
func ensureRuntimeDirectory(staging, rel string) error {
	joined, err := safeJoin(staging, rel)
	if err != nil {
		return err
	}
	// The final mountpoint must be a real directory. Check the lexical path
	// before resolveWithin follows ancestor symlinks; otherwise a final image
	// symlink (for example /tmp -> /var/tmp) would look like an ordinary target
	// and could not be safely overmounted after the artifact is mounted.
	if info, statErr := os.Lstat(joined); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path exists but is a symlink")
		}
		if !info.IsDir() {
			return fmt.Errorf("path exists but is not a directory")
		}
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	path, err := resolveWithin(staging, rel)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.IsDir() {
			return fmt.Errorf("path exists but is not a directory")
		}
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return mkdirImageDirectories(path, 0o755)
}

// publishExt4 mkfs-es the staging tree into a temp file (or directly into
// OutImage in the legacy path) and then either renames into OutImage or
// streams into the storage backend. The tmp-file indirection keeps mkfs
// happy (it insists on a path, not stdout) and lets us atomically publish
// to the storage backend via Put.
//
// The temp file is removed before returning; the caller sees no scratch
// left behind even on error.
func (b *Builder) publishExt4(ctx context.Context, in BuildInput, staging string, sizeMB int, limits api.Limits) (int, ArtifactIdentity, error) {
	if in.OutImage != "" {
		// Legacy path. Mkfs writes directly to OutImage; the caller's
		// filesystem already provides atomicity (or it doesn't, and we
		// honour that — pre-#96 production). Kept for the integration
		// test.
		if err := os.MkdirAll(filepath.Dir(in.OutImage), 0o755); err != nil {
			return 0, ArtifactIdentity{}, fmt.Errorf("rootfs: mkdir out dir: %w", err)
		}
		sizeMB, err := b.runAppMkfs(ctx, staging, in.OutImage, sizeMB, limits)
		if err != nil {
			return 0, ArtifactIdentity{}, err
		}
		identity, err := artifactIdentityFromPath(ctx, in.OutImage)
		return sizeMB, identity, err
	}
	// Storage path. Mkfs into a sibling temp file, then Put the bytes
	// under StorageKey and remove the temp.
	tmp, err := os.CreateTemp(filepath.Dir(staging), "faas-mkfs-*.ext4")
	if err != nil {
		return 0, ArtifactIdentity{}, fmt.Errorf("rootfs: create tmp ext4: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Close(); err != nil {
		return 0, ArtifactIdentity{}, fmt.Errorf("rootfs: close tmp ext4: %w", err)
	}
	sizeMB, err = b.runAppMkfs(ctx, staging, tmpPath, sizeMB, limits)
	if err != nil {
		return 0, ArtifactIdentity{}, err
	}
	// nolint:forbidigo // tmpPath is from os.MkdirTemp at the top of
	// this function — a daemon-internal scratch file the builder just
	// wrote via MkfsCommand. Not a customer path.
	f, err := os.Open(tmpPath)
	if err != nil {
		return 0, ArtifactIdentity{}, fmt.Errorf("rootfs: open mkfs output: %w", err)
	}
	closed = true // release the open file before Put; storage Put closes the file via defer elsewhere
	defer func() { _ = f.Close() }()
	identity, err := publishArtifactIdentity(ctx, in.Storage, in.StorageKey, f)
	if err != nil {
		return 0, ArtifactIdentity{}, fmt.Errorf("rootfs: publish %q: %w", in.StorageKey, err)
	}
	// ADR-038: sign the published ext4 so schedd's cold-boot verify
	// (pkg/cosign.LocalVerifier) can detect tampering. Signing
	// failure is build-fatal — an unsigned layer cannot be cold-
	// booted; the verify side rejects with code=sig_invalid. We
	// don't surface a softer fallback (e.g. skip signing and let
	// the customer hit 503 on first wake) because the gap from
	// "no signature written" to "schedd accepted the unsigned
	// layer" is the compromise path the sig is meant to close.
	if b.signer != nil {
		sigKey := "sigs/" + in.StorageKey + ".sig"
		if err := b.signer.Sign(ctx, in.StorageKey, sigKey); err != nil {
			return 0, ArtifactIdentity{}, fmt.Errorf("rootfs: sign %q: %w", in.StorageKey, err)
		}
	}
	return sizeMB, identity, nil
}

// emitSBOM runs the injected SBOM subprocess against the staging
// directory and Persists the CycloneDX JSON to Storage under
// SBOMStorageKey (issue #299 / ADR-038 Phase 3). The build is
// best-effort: a syft error, empty output, or non-JSON output
// leaves SBOMKey empty and the build still succeeds (the build
// itself is the load-bearing artefact; the SBOM is observational).
//
// The staging dir is the only artefact that contains the customer's
// source tree at this point — the cleanup defer at the top of Build
// has not yet fired. Running syft on the produced ext4 instead
// would require rootfs to parse ext4 in-process to enumerate the
// files syft understands, which would duplicate syft's own
// extraction logic. The staging-dir approach is also what the
// issue-spec recommends: it picks up /etc/faas/app.json, the
// function runner binary, and the customer's source tarball
// directly without an extra mkfs round-trip.
func (b *Builder) emitSBOM(ctx context.Context, in BuildInput, staging string) (string, error) {
	if in.SBOMRun == nil {
		return "", nil
	}
	if in.Storage == nil || in.SBOMStorageKey == "" {
		// Caller wired SBOMRun but not the storage side. Fail soft
		// rather than aborting the build — the SBOM is observational.
		return "", nil
	}
	// SBOM emission is best-effort: a failed syft run, an empty
	// payload, an invalid JSON document, or a transient storage write
	// failure must NOT regress the build (the ext4 itself is the
	// load-bearing artefact; the SBOM is observational). The errors
	// on the two non-returned paths are swallowed with an explicit
	// nolint annotation so future readers don't mistake the swallow
	// for a missed return.
	blob, _ := in.SBOMRun(ctx, staging) //nolint:nilerr
	if len(blob) == 0 {
		return "", nil
	}
	if !json.Valid(blob) {
		return "", nil
	}
	if putErr := in.Storage.Put(ctx, in.SBOMStorageKey, bytes.NewReader(blob)); putErr != nil {
		//nolint:nilerr
		return "", nil
	}
	return in.SBOMStorageKey, nil
}

// validateOutputTarget enforces the rule that exactly one of {Storage +
// StorageKey, OutImage} is set. Without this guard a misconfigured caller
// would silently fall back to OutImage="" and write into the cwd, or
// silently drop the produced ext4 on Storage="" + StorageKey="x".
func validateOutputTarget(in BuildInput) error {
	hasStorage := in.Storage != nil && in.StorageKey != ""
	hasOut := in.OutImage != ""
	switch {
	case hasStorage && hasOut:
		return errors.New("rootfs: BuildInput has both Storage and OutImage set; pick one")
	case !hasStorage && !hasOut:
		return errors.New("rootfs: BuildInput has neither Storage nor OutImage set")
	case hasStorage && in.StorageKey == "":
		return errors.New("rootfs: BuildInput has Storage but empty StorageKey")
	case hasStorage && in.Storage == nil:
		return errors.New("rootfs: BuildInput has StorageKey but nil Storage")
	}
	return nil
}

// InjectManifest writes the app.json contract to /etc/faas/app.json in staging.
func InjectManifest(staging string, m api.AppManifest) error {
	path := filepath.Join(staging, filepath.FromSlash(api.AppManifestPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("rootfs: manifest dir: %w", err)
	}
	var buf bytes.Buffer
	if err := api.WriteManifest(&buf, m); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("rootfs: write manifest: %w", err)
	}
	return nil
}

// InjectWorkloadManifest writes the effective runtime contract for one
// workload into its immutable layer. The name is validated before it is used
// in a filesystem path so a malformed wire value cannot escape the staging
// tree. The guest reads this file after overlay assembly and uses it only for
// the matching sidecar; the deployment roster remains the source of memory,
// port, and essential-policy metadata.
func InjectWorkloadManifest(staging, workloadName string, m api.AppManifest) error {
	if err := validateWorkloadManifestName(workloadName); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("rootfs: workload manifest: %w", err)
	}
	path := filepath.Join(staging, filepath.FromSlash(strings.TrimPrefix(api.SidecarWorkloadManifestPath, "/")), workloadName, "workload.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("rootfs: workload manifest dir: %w", err)
	}
	var buf bytes.Buffer
	if err := api.WriteManifest(&buf, m); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("rootfs: write workload manifest: %w", err)
	}
	return nil
}

func validateWorkloadManifestName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || len(name) > 63 {
		return fmt.Errorf("rootfs: invalid workload manifest name %q", name)
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("rootfs: invalid workload manifest name %q", name)
		}
	}
	first := name[0]
	if (first < 'a' || first > 'z') && (first < '0' || first > '9') {
		return fmt.Errorf("rootfs: invalid workload manifest name %q", name)
	}
	return nil
}

// InjectGuestInit copies the guest-init binary into staging as /sbin/init (PID 1,
// spec §4.8), executable.
func InjectGuestInit(staging, guestInitPath string) error {
	_, err := injectGuestInitWithDigest(staging, guestInitPath)
	return err
}

func injectGuestInitWithDigest(staging, guestInitPath string) (string, error) {
	if guestInitPath == "" {
		return "", fmt.Errorf("rootfs: empty guest-init path")
	}
	data, err := os.ReadFile(guestInitPath)
	if err != nil {
		return "", fmt.Errorf("rootfs: read guest-init: %w", err)
	}
	dst := filepath.Join(staging, "sbin", "init")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	// OCI base images commonly ship /sbin/init as a symlink (for example
	// Alpine points it at /bin/busybox). Remove the link before writing the
	// platform PID-1 binary; os.WriteFile follows a symlink and would
	// overwrite the link target while leaving /sbin/init pointing at it.
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("rootfs: remove existing init: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return "", fmt.Errorf("rootfs: write guest-init: %w", err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

// ApplyTarball unpacks a customer source tarball at /app. Archives produced
// by the CLI contain one project-root directory (for example
// function-node/handler.js); that transport wrapper is stripped so the guest
// sees the source at /app/handler.js. A flat archive remains flat.
// Path-escape protection reuses the ApplyLayerGz allowlist so a malicious
// tarball can't escape /app.
//
// capBytes is the cumulative unpacked-size cap (issue #197 B3.7). The
// apid-side upload cap (SourceTarballMaxMB) guards the ON-DISK tarball
// size, but a gzipped tarball can expand dramatically when unpacked
// (the customer uploads node_modules and the layer blows past the
// drive1 cap). Pass 0 to skip the cap (legacy callers + tests).
//
// Returns ErrTarballExceedsCap (errors.As-detectable) when the
// cumulative unpacked bytes exceed capBytes. The cap is enforced
// after each entry so a 10 GB tarball is rejected without writing the
// full 10 GB to disk — the per-entry io.CopyN in applyEntry is the
// inner bound, this is the outer bound. The cap is on DECLARED tar
// header sizes, not actual streamed bytes: a malicious tarball
// declaring hdr.Size=1 with a 1 GB body would only consume the
// declared 1 byte toward the cap (the io.CopyN is the inner
// streamer-side guard). Build() above promotes the sentinel to
// api.ErrAppLayerTooLarge(*Problem) with the plan in context;
// direct callers (e.g. unit tests) can errors.As against
// *ErrTarballExceedsCap to inspect WrittenBytes / EntryBytes.
//
//nolint:forbidigo // tarballPath is the apid-spooled path under spoolRoot() that already passed apid's validateTarballShape (in cmd/apid/deploy_inputs.go) — bytes are validated before builderd opens them; symlink-attack on the open itself is impossible because apid wrote the file via os.Create above with a fresh random id. The "customer" framing in the doc comment refers to the *contents* of the tarball (handler code), not the file path on disk.
func ApplyTarball(staging, tarballPath string, capBytes int64) error {
	prefix, err := tarball.RootPrefix(tarballPath)
	if err != nil {
		return fmt.Errorf("rootfs: inspect tarball: %w", err)
	}
	f, err := os.Open(tarballPath)
	if err != nil {
		return fmt.Errorf("rootfs: open tarball: %w", err)
	}
	defer func() { _ = f.Close() }()
	appDir := filepath.Join(staging, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}
	return applyTarballWithCap(appDir, f, capBytes, prefix)
}

// ErrTarballExceedsCap is the sentinel returned by ApplyTarball when a
// function tarball's cumulative unpacked bytes exceed the cap. Callers
// can errors.As against it to translate to the plan-scoped
// api.ErrAppLayerTooLarge Problem.
//
// The cap is on DECLARED tar header sizes (issue #197 B3.7); a
// tarball whose entries lie about hdr.Size would under-report toward
// the cap but still be bounded by the per-entry io.CopyN inner
// guard. See ApplyTarball's doc for the full story.
type ErrTarballExceedsCap struct {
	WrittenBytes int64
	EntryBytes   int64
	CapBytes     int64
}

func (e *ErrTarballExceedsCap) Error() string {
	return fmt.Sprintf("function tarball exceeds cap: %d bytes written, %d declared in next entry, %d cap", e.WrittenBytes, e.EntryBytes, e.CapBytes)
}

// Is implements errors.Is so callers can promote with errors.Is(err, &ErrTarballExceedsCap{}).
func (e *ErrTarballExceedsCap) Is(target error) bool {
	_, ok := target.(*ErrTarballExceedsCap)
	return ok
}

// layerApplyError wraps a failure to apply image layer i. Exceeding the
// extraction budget is reported as the plan's app-layer Problem so the
// customer sees the limit and the observed size, as for the final cap.
func layerApplyError(limits api.Limits, i int, err error) error {
	var budgetErr *ErrLayerExtractionExceedsBudget
	if errors.As(err, &budgetErr) {
		return api.ErrAppLayerTooLarge(limits, budgetErr.UsedBytes+budgetErr.EntryBytes)
	}
	var entriesErr *ErrLayerExtractionTooManyEntries
	if errors.As(err, &entriesErr) {
		return api.ErrAppLayerTooManyEntries(limits, entriesErr.LimitEntries)
	}
	return fmt.Errorf("rootfs: apply layer %d: %w", i, err)
}

// applyTarballWithCap is the cap-aware variant of ApplyTarball. It
// streams the tar, accumulates each entry's declared Size into
// `written`, and bails with *ErrTarballExceedsCap once the running
// total overshoots capBytes. Symlinks and char devices contribute 0
// bytes (no file system allocation); the cap is on the on-disk size
// post-unpack, which is what the AppLayerMaxMB limit already
// constrains.
func applyTarballWithCap(dst string, r io.Reader, capBytes int64, prefix string) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("rootfs: gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	var written int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("rootfs: read tar: %w", err)
		}
		// Symlinks / char devices / fifos / hardlinks don't allocate
		// on-disk bytes for the consumer's quota. Cap is on the
		// post-unpack size, which is what AppLayerMaxMB enforces.
		if hdr.Typeflag == tar.TypeReg {
			if capBytes > 0 && written+hdr.Size > capBytes {
				return &ErrTarballExceedsCap{
					WrittenBytes: written,
					EntryBytes:   hdr.Size,
					CapBytes:     capBytes,
				}
			}
			written += hdr.Size
		}
		archiveName := tarball.TrimLeadingDotSlash(hdr.Name)
		if prefix != "" {
			archiveName = strings.TrimSuffix(archiveName, "/")
			if archiveName == prefix {
				// The archive's wrapper directory is transport metadata,
				// not customer content.
				continue
			}
			if strings.HasPrefix(archiveName, prefix+"/") {
				archiveName = strings.TrimPrefix(archiveName, prefix+"/")
			}
		}
		// Keep every staging filesystem operation inside the positive
		// archive-name validation branch. resolveEntryPath then adds
		// containment and ancestor-symlink protection.
		if !strings.Contains(archiveName, "..") {
			hdr.Name = archiveName
			// resolveEntryPath rejects absolute names and clamps ancestor
			// symlinks inside the staging root before applyEntry can create
			// or replace an entry.
			target, err := resolveEntryPath(dst, archiveName)
			if err != nil {
				return err
			}
			if err := applyEntry(dst, target, hdr, tr, nil); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("rootfs: archive entry %q contains traversal marker", hdr.Name)
	}
}

// NormalizeFunctionHandler makes the source filename agree with the runtime
// runner's manifest path. The public function contract asks customers for an
// exported handler(event, ctx) or a Fetch API export ({ fetch }), while the
// low-level runner consumes the §4.9 stdin/stdout envelope. For exported
// Node/Python handlers, generate a small protocol adapter at the manifest path
// and keep the customer's source filename intact. Protocol-style handlers
// remain supported for compatibility and are still aliased exactly as before.
func NormalizeFunctionHandler(staging, handlerPath string) error {
	clean := filepath.ToSlash(filepath.Clean(handlerPath))
	if !strings.HasPrefix(clean, "/app/") || clean == "/app/" {
		return fmt.Errorf("rootfs: invalid function handler path %q", handlerPath)
	}
	target := filepath.Join(staging, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	if info, err := os.Stat(target); err == nil {
		if info.IsDir() {
			return fmt.Errorf("rootfs: function handler path %q is a directory", handlerPath)
		}
		// Python functions use /app/handler.py as both the public source
		// name and the runner's manifest path. Wrap only the documented
		// handler(event, ctx) shape; an existing protocol script is left
		// untouched.
		if filepath.Ext(target) == ".py" {
			data, readErr := os.ReadFile(target)
			if readErr != nil {
				return fmt.Errorf("rootfs: read python function source %q: %w", target, readErr)
			}
			if isPythonFunctionSource(string(data)) {
				return wrapPythonFunctionHandler(target, data)
			}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rootfs: stat function handler %q: %w", handlerPath, err)
	}
	if filepath.Ext(target) != ".js" && filepath.Ext(target) != ".py" && clean != "/app/handler" {
		return fmt.Errorf("rootfs: function handler %q not found", handlerPath)
	}
	sourceName := "handler.js"
	if filepath.Ext(target) == ".py" {
		sourceName = "handler.py"
	} else if clean == "/app/handler" {
		// Railpack's Go plan emits the compiled function binary as
		// /app/server. The Go runner keeps the stable function contract
		// /app/handler, so source-build OCI layers are normalized here.
		sourceName = "server"
	}
	source := filepath.Join(filepath.Dir(target), sourceName)
	if source == target {
		return fmt.Errorf("rootfs: function handler %q not found", handlerPath)
	}
	if sourceInfo, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rootfs: function handler %q not found", handlerPath)
		}
		return fmt.Errorf("rootfs: stat function source %q: %w", source, err)
	} else if sourceInfo.IsDir() {
		return fmt.Errorf("rootfs: function source %q is a directory", source)
	}
	if clean == "/app/handler" {
		// Go's source-build artifact is already the executable; avoid reading
		// the binary into memory just to apply the stable handler alias.
		if err := os.Rename(source, target); err != nil {
			return fmt.Errorf("rootfs: alias function handler %s as %s: %w", source, target, err)
		}
		return nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("rootfs: read function source %q: %w", source, err)
	}
	if filepath.Ext(target) == ".js" && isNodeFunctionSource(string(data)) {
		if err := os.WriteFile(target, []byte(nodeFunctionAdapter), 0o644); err != nil {
			return fmt.Errorf("rootfs: write node function adapter %s: %w", target, err)
		}
		return nil
	}
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("rootfs: alias function handler %s as %s: %w", source, target, err)
	}
	return nil
}

// NormalizeFunctionHandlerFrom makes an already-built handler agree with the
// runtime runner's manifest path. Unlike NormalizeFunctionHandler, the source
// and its dependencies have already been applied from an OCI build artifact;
// only the handler entrypoint needs to be copied/aliased. Keeping this path
// separate prevents a source tarball from overwriting the dependency-complete
// Railpack output.
func NormalizeFunctionHandlerFrom(staging, sourcePath, handlerPath string) error {
	sourceClean, err := cleanFunctionPath(sourcePath, "source")
	if err != nil {
		return err
	}
	targetClean, err := cleanFunctionPath(handlerPath, "target")
	if err != nil {
		return err
	}
	source := filepath.Join(staging, filepath.FromSlash(strings.TrimPrefix(sourceClean, "/")))
	target := filepath.Join(staging, filepath.FromSlash(strings.TrimPrefix(targetClean, "/")))
	info, err := os.Stat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rootfs: function handler source %q not found", sourcePath)
		}
		return fmt.Errorf("rootfs: stat function handler source %q: %w", sourcePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("rootfs: function handler source %q is a directory", sourcePath)
	}
	if source == target {
		return normalizeExistingFunctionHandler(target, targetClean)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("rootfs: create function handler target directory: %w", err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("rootfs: read function handler source %q: %w", sourcePath, err)
	}
	// The adapter imports the customer's file where it lives, so a handler in
	// a shared source tree keeps its relative imports (ADR-985).
	handlerRel, err := filepath.Rel(filepath.Dir(target), source)
	if err != nil {
		return fmt.Errorf("rootfs: locate function handler source %q: %w", sourcePath, err)
	}
	handlerRel = filepath.ToSlash(handlerRel)
	if filepath.Ext(target) == ".js" && isNodeFunctionSource(string(data)) {
		if err := os.WriteFile(target, []byte(nodeFunctionAdapterFor(handlerRel)), 0o644); err != nil {
			return fmt.Errorf("rootfs: write node function adapter %s: %w", target, err)
		}
		return nil
	}
	if filepath.Ext(target) == ".py" && isPythonFunctionSource(string(data)) {
		if err := os.WriteFile(target, []byte(pythonFunctionAdapterFor(handlerRel)), 0o644); err != nil {
			return fmt.Errorf("rootfs: write python function adapter %s: %w", target, err)
		}
		return nil
	}
	mode := info.Mode().Perm()
	if err := os.WriteFile(target, data, mode); err != nil {
		return fmt.Errorf("rootfs: write function handler target %q: %w", handlerPath, err)
	}
	if err := os.Chmod(target, mode); err != nil {
		return fmt.Errorf("rootfs: preserve function handler mode %q: %w", handlerPath, err)
	}
	return normalizeExistingFunctionHandler(target, targetClean)
}

func cleanFunctionPath(path, label string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(clean, "/app/") || clean == "/app/" {
		return "", fmt.Errorf("rootfs: invalid function handler %s path %q", label, path)
	}
	return clean, nil
}

func normalizeExistingFunctionHandler(target, handlerPath string) error {
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("rootfs: stat function handler %q: %w", handlerPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("rootfs: function handler path %q is a directory", handlerPath)
	}
	if filepath.Ext(target) != ".py" {
		return nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("rootfs: read python function source %q: %w", target, err)
	}
	if isPythonFunctionSource(string(data)) {
		return wrapPythonFunctionHandler(target, data)
	}
	return nil
}

func isNodeFunctionSource(source string) bool {
	if !strings.Contains(source, "export") {
		return false
	}
	// The legacy public shape exports handler(event, ctx). The Fetch API
	// shape exports an object with fetch(request, env, ctx). Both are
	// explicit function contracts, so either marker warrants the adapter.
	return strings.Contains(source, "handler") || strings.Contains(source, "fetch")
}

func isPythonFunctionSource(source string) bool {
	return strings.Contains(source, "def handler(") || strings.Contains(source, "async def handler(")
}

// nodeFunctionAdapter translates the public handler(event, ctx) and Fetch API
// ({ fetch }) contracts to the runner's protocol envelope. It lives in the app
// layer so the runner can remain a deliberately tiny, protocol-only binary
// shared by all Node apps.
const nodeFunctionAdapter = `// FAAS_PERSISTENT_PROTOCOL_V1
(async () => {
const pathModule = await import("node:path");
const urlModule = await import("node:url");
const readlineModule = await import("node:readline");
const path = pathModule.default || pathModule;

// Customer logs belong on stderr; stdout is reserved for newline-framed
// response envelopes consumed by faas-runner.
console.log = console.error;
console.info = console.error;
const handlerFile = path.join(path.dirname(process.argv[1]), "handler.js");
const mod = await import(urlModule.pathToFileURL(handlerFile).href);
const defaultExport = mod.default;
const eventHandler = mod.handler || (typeof defaultExport === "function" ? defaultExport : null);
const fetchHandler = mod.fetch || (defaultExport && typeof defaultExport.fetch === "function"
  ? defaultExport.fetch.bind(defaultExport)
  : null);
if (typeof eventHandler !== "function" && typeof fetchHandler !== "function") {
  throw new Error("handler.js must export handler/default or default { fetch }");
}
// A worker serves FAAS_FUNCTION_CONCURRENCY requests at once when the app
// sets function_concurrency (ADR-1342); requests and every response line
// then carry the runner's __faas_id.
const concurrency = process.env.FAAS_PERSISTENT_WORKER === "1"
  ? Math.min(Math.max(Number.parseInt(process.env.FAAS_FUNCTION_CONCURRENCY || "1", 10) || 1, 1), 256)
  : 1;
if (process.env.FAAS_PERSISTENT_WORKER === "1") {
  process.stdout.write(JSON.stringify({ __faas_ready: true, concurrency }) + "\n");
}

// Response frames (ADR-999): one line is a complete response; a "head" line
// starts a streamed one that "chunk" lines continue and "end" closes. out is
// one request's output: its id, whether its stream has started, and whether
// the runner cancelled it.
const output = (id) => {
  const out = { streaming: false, cancelled: false, controller: new AbortController() };
  out.emit = (frame) => {
    if (!out.cancelled) process.stdout.write(JSON.stringify(id === undefined ? frame : { __faas_id: id, ...frame }) + "\n");
  };
  return out;
};
const toBuffer = (piece) => Buffer.isBuffer(piece) ? piece
  : piece instanceof Uint8Array ? Buffer.from(piece.buffer, piece.byteOffset, piece.byteLength)
  : Buffer.from(String(piece));
// Event handlers read headers as Node does (req.headers["x-api-key"]), but
// the envelope carries Go's canonical names ("X-Api-Key"), so every
// lowercase read was undefined (hunt #10, H10-20). Keys are lowercase and
// lookups ignore case, so canonical-case reads keep working.
function caseInsensitiveHeaders(raw) {
  const lower = {};
  for (const [key, value] of Object.entries(raw || {})) lower[key.toLowerCase()] = value;
  return new Proxy(lower, {
    get: (target, prop) => typeof prop === "string" ? target[prop.toLowerCase()] : target[prop],
    has: (target, prop) => typeof prop === "string" ? prop.toLowerCase() in target : prop in target,
  });
}
const emitWhole = (out, status, headers, bytes) => out.emit({ status, headers, body_b64: bytes.toString("base64") });
const emitHead = (out, status, headers) => { out.streaming = true; out.emit({ frame: "head", status, headers }); };
const emitChunk = (out, bytes) => { if (bytes.length > 0) out.emit({ frame: "chunk", body_b64: bytes.toString("base64") }); };
const isStreamBody = (body) => body !== null && typeof body === "object" && !Buffer.isBuffer(body) &&
  !(body instanceof Uint8Array) && typeof body[Symbol.asyncIterator] === "function";
async function streamIterable(out, status, headers, body) {
  emitHead(out, status, headers);
  for await (const piece of body) {
    if (out.cancelled) return;
    emitChunk(out, toBuffer(piece));
  }
  out.emit({ frame: "end" });
}
// A Fetch API body already complete within one turn of the event loop is
// sent as one response, exactly as before; a body still being produced
// (server-sent events, generated text) is streamed as it arrives.
async function respondFetchBody(out, status, headers, body) {
  if (!body) { emitWhole(out, status, headers, Buffer.alloc(0)); return; }
  const reader = body.getReader();
  let cancelling;
  const abort = () => { cancelling ||= reader.cancel().catch(() => {}); };
  out.controller.signal.addEventListener("abort", abort, { once: true });
  if (out.controller.signal.aborted) abort();
  try {
  const pending = Symbol("pending");
  const buffered = [];
  let read = reader.read();
  for (;;) {
    const next = await Promise.race([read, new Promise((resolve) => setImmediate(resolve, pending))]);
    if (next === pending) break;
    if (next.done) { emitWhole(out, status, headers, Buffer.concat(buffered)); return; }
    buffered.push(toBuffer(next.value));
    read = reader.read();
  }
  emitHead(out, status, headers);
  emitChunk(out, Buffer.concat(buffered));
  for (let next = await read; !next.done; next = await reader.read()) {
    if (out.cancelled) { abort(); return; }
    emitChunk(out, toBuffer(next.value));
  }
  out.emit({ frame: "end" });
  } finally {
    out.controller.signal.removeEventListener("abort", abort);
    if (cancelling) await cancelling;
    reader.releaseLock();
  }
}

// handleOne answers one request: input is the raw line, or the decoded
// request on a shared worker.
async function handleOne(input, out) {
  let invocationID = "";
  try {
  const env = typeof input === "string" ? JSON.parse(input) : input;
  const raw = Buffer.from(env.body_b64 || "", "base64").toString("utf8");
  let body = raw;
  if (raw === "") {
    body = null;
  } else {
    try { body = JSON.parse(raw); } catch (_) {}
  }
  const headers = env.headers || {};
  invocationID = headers["x-faas-invocation-id"] || headers["X-Faas-Invocation-Id"] || "";
  // One line per call, like the Python adapter: console.error(msg, fields)
  // printed a multi-line inspect() of the fields, and every line became its
  // own log record (H10-59).
  const formatLogArg = (arg) => {
    if (typeof arg === "string") return arg;
    if (arg instanceof Error) return arg.stack || String(arg);
    try {
      const text = JSON.stringify(arg);
      if (text !== undefined) return text;
    } catch (_) {}
    return String(arg);
  };
  const log = {};
  for (const level of ["debug", "info", "warn", "error"]) {
    log[level] = (...args) => console.error(args.map(formatLogArg).join(" "));
  }
  const ctx = {
    invocation_id: invocationID,
    lifecycle_hook: env.lifecycle_hook || "",
    signal: out.controller.signal,
    log,
    // Workers-style handlers commonly call ctx.waitUntil(). The runner's
    // existing tail host is fed by its runtime shim; keep this adapter
    // compatible when that shim is present and otherwise retain the
    // promise's rejection on stderr instead of crashing the worker.
    waitUntil(promise) {
      if (promise && typeof promise.then === "function") {
        Promise.resolve(promise).catch((err) => console.error(err));
      }
    },
  };
  let value;
  if (typeof fetchHandler === "function") {
    const method = env.method || "POST";
    const requestURL = new URL(env.path || "/", "http://faas.local");
    if (env.query) requestURL.search = env.query.startsWith("?") ? env.query : "?" + env.query;
    const requestInit = { method, headers, signal: ctx.signal };
    const requestBody = Buffer.from(env.body_b64 || "", "base64");
    if (method !== "GET" && method !== "HEAD" && requestBody.length > 0) requestInit.body = requestBody;
    const request = new Request(requestURL, requestInit);
    value = await fetchHandler(request, { ...process.env }, ctx);
  } else {
    const event = {
      method: env.method || "POST",
      path: env.path || "/",
      headers: caseInsensitiveHeaders(headers),
      query: env.query || "",
      body,
      // Spec §4.9: the exact request bytes. body is parsed JSON when the
      // payload is JSON, which a signature check cannot use.
      body_b64: env.body_b64 || "",
    };
    value = await eventHandler(event, ctx);
  }
  if (value && typeof value === "object" && typeof value.arrayBuffer === "function" &&
      typeof value.status === "number" && value.headers) {
    // Every Set-Cookie line survives (ADR-999); other repeated headers are
    // already combined by the Headers object.
    const fetchHeaders = {};
    for (const [key, item] of value.headers.entries()) {
      if (key.toLowerCase() !== "set-cookie") fetchHeaders[key] = item;
    }
    const cookies = typeof value.headers.getSetCookie === "function" ? value.headers.getSetCookie() : [];
    if (cookies.length > 0) fetchHeaders["set-cookie"] = cookies;
    await respondFetchBody(out, value.status, fetchHeaders, value.body);
    return;
  }
  let status = 200;
  let responseHeaders = {};
  let responseBody = value;
  if (value && typeof value === "object" && !Buffer.isBuffer(value)) {
    status = Number(value.statusCode ?? value.status ?? 200);
    responseHeaders = value.headers || {};
    if (Object.prototype.hasOwnProperty.call(value, "body")) responseBody = value.body;
  }
  // A header value may be a list, one line per element (ADR-999).
  const normalizedHeaders = {};
  for (const [key, item] of Object.entries(responseHeaders)) {
    normalizedHeaders[key] = Array.isArray(item) ? item.map(String) : String(item);
  }
  // An async iterable body (async generator, Node stream) is streamed.
  if (isStreamBody(responseBody)) {
    await streamIterable(out, status, normalizedHeaders, responseBody);
    return;
  }
  if (responseBody === undefined || responseBody === null) responseBody = "";
  if (typeof responseBody !== "string" && !Buffer.isBuffer(responseBody)) {
    responseBody = JSON.stringify(responseBody);
  }
  emitWhole(out, status, normalizedHeaders, Buffer.from(responseBody));
  } catch (err) {
    if (out.cancelled) return;
    const rawMessage = err instanceof Error ? err.message : String(err);
    const message = rawMessage.length > 256 ? rawMessage.slice(0, 256) + "…" : rawMessage;
    console.error(err && err.stack ? err.stack : err);
    // The status is already sent once a stream has started; the error
    // frame tells the runner to abort the response.
    if (out.streaming) {
      out.emit({ frame: "error", message });
      return;
    }
    const responseBody = JSON.stringify({
      error: "handler_error",
      message,
      invocation_id: invocationID,
    });
    out.emit({
      status: 500,
      headers: { "content-type": "application/json; charset=utf-8" },
      body_b64: Buffer.from(responseBody).toString("base64"),
    });
  }
}

// Cancellation suppresses output and aborts the request's I/O signal. Ack only
// after its handler and stream have actually stopped; the runner retires an
// uncooperative worker instead of handing its occupied slot to another request.
const inflight = new Map();
const lines = readlineModule.createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const line of lines) {
  if (!line.trim()) continue;
  if (concurrency <= 1) {
    await handleOne(line, output(undefined));
    continue;
  }
  let request;
  try { request = JSON.parse(line); } catch (err) { console.error(err); continue; }
  if (typeof request.__faas_cancel === "string") {
    const id = request.__faas_cancel;
    const out = inflight.get(id);
    if (out) {
      out.cancelled = true;
      out.controller.abort();
      if (!out.cancelling) {
        out.cancelling = true;
        void out.done.then(() => process.stdout.write(JSON.stringify({ __faas_cancelled: id }) + "\n"));
      }
    } else process.stdout.write(JSON.stringify({ __faas_cancelled: id }) + "\n");
    continue;
  }
  const id = String(request.__faas_id);
  const out = output(id);
  inflight.set(id, out);
  out.done = handleOne(request, out).finally(() => inflight.delete(id));
}
})().catch((err) => {
  console.error(err && err.stack ? err.stack : err);
  process.exitCode = 1;
});
`

const pythonFunctionAdapter = `# FAAS_PERSISTENT_PROTOCOL_V1
import asyncio
import base64
import concurrent.futures
import importlib.util
import inspect
import json
import os
import sys
import threading
import traceback

class _Log:
    def info(self, *args, **kwargs):
        # Match the stdlib logging call shape used by the stock templates.
        # Passing extra through to print raises TypeError and suppresses the
        # handler's response; serialize it as structured context instead.
        extra = kwargs.pop("extra", None)
        kwargs.pop("exc_info", None)
        kwargs.pop("stack_info", None)
        message = " ".join(str(arg) for arg in args)
        if isinstance(extra, dict) and extra:
            message += " " + json.dumps(extra, sort_keys=True, default=str)
        print(message, file=sys.stderr, flush=True)
    debug = info
    warning = info
    warn = info
    error = info

# stdout is protocol-bearing. Route ordinary customer prints — including
# module-level prints during import — to stderr before loading customer code.
real_stdout = sys.stdout
sys.stdout = sys.stderr
handler_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".faas-handler.py")
spec = importlib.util.spec_from_file_location("faas_handler_impl", handler_path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
handler = getattr(module, "handler", None)
if not callable(handler): raise RuntimeError("handler.py must define handler(event, ctx)")
# A worker serves FAAS_FUNCTION_CONCURRENCY requests at once when the app
# sets function_concurrency (ADR-1342); requests and every response line then
# carry the runner's __faas_id. An async handler runs each request as a task;
# a sync handler runs each on its own thread.
handler_is_async = inspect.iscoroutinefunction(handler)
concurrency = 1
if os.environ.get("FAAS_PERSISTENT_WORKER") == "1":
    try:
        concurrency = min(max(int(os.environ.get("FAAS_FUNCTION_CONCURRENCY", "1")), 1), 256)
    except ValueError:
        concurrency = 1
if os.environ.get("FAAS_PERSISTENT_WORKER") == "1":
    real_stdout.write(json.dumps({"__faas_ready": True, "concurrency": concurrency}) + "\n")
    real_stdout.flush()

# One event loop for the life of the worker. asyncio.run() per request
# closed the loop after every invocation, so async clients a handler keeps
# between requests (DB pools, HTTP sessions, queues) were bound to a dead
# loop and the next warm request failed with "bound to a different event
# loop" / "Event loop is closed".
loop = asyncio.new_event_loop()
asyncio.set_event_loop(loop)

class _Context:
    log = _Log()
    def __init__(self, invocation, cancelled=None, lifecycle_hook=""):
        self.invocation_id = invocation
        self.cancelled = cancelled if cancelled is not None else threading.Event()
        self.lifecycle_hook = lifecycle_hook

# Response frames (ADR-999): one line is a complete response; a "head" line
# starts a streamed one that "chunk" lines continue and "end" closes.
def _emit(frame):
    real_stdout.write(json.dumps(frame) + "\n")
    real_stdout.flush()

def _to_bytes(piece):
    if isinstance(piece, (bytes, bytearray)):
        return bytes(piece)
    return str(piece).encode("utf-8")

def _emit_chunk(piece, emit=_emit):
    data = _to_bytes(piece)
    if data:
        emit({"frame": "chunk", "body_b64": base64.b64encode(data).decode("ascii")})

async def _drain_async(body, emit=_emit):
    async for piece in body:
        _emit_chunk(piece, emit)

def _is_stream_body(body):
    # A generator, async generator or other iterator streams; str, bytes,
    # dicts and lists are complete bodies.
    if isinstance(body, (str, bytes, bytearray, dict, list, tuple)):
        return False
    return hasattr(body, "__aiter__") or (hasattr(body, "__iter__") and hasattr(body, "__next__"))

class _Headers(dict):
    # Lowercase keys, case-insensitive lookups: the envelope carries Go's
    # canonical names, so headers["x-api-key"] missed (H10-20).
    def __init__(self, raw):
        super().__init__((str(k).lower(), v) for k, v in raw.items())
    def __getitem__(self, key): return super().__getitem__(str(key).lower())
    def __contains__(self, key): return super().__contains__(str(key).lower())
    def get(self, key, default=None): return super().get(str(key).lower(), default)

def _event(env):
    body_b64 = env.get("body_b64", "") or ""
    # Lossy for non-UTF-8 payloads (matching the Node adapter); the exact
    # bytes stay available to the handler as event["body_b64"].
    raw = base64.b64decode(body_b64).decode("utf-8", errors="replace")
    if raw == "":
        body = None
    else:
        try:
            body = json.loads(raw)
        except json.JSONDecodeError:
            body = raw
    headers = env.get("headers") or {}
    invocation_id = headers.get("x-faas-invocation-id", headers.get("X-Faas-Invocation-Id", ""))
    event = {
        "method": env.get("method") or "POST",
        "path": env.get("path") or "/",
        "headers": _Headers(headers),
        "query": env.get("query") or "",
        "body": body,
        "body_b64": body_b64,
    }
    return event, invocation_id

def _shape(result):
    status = 200
    response_headers = {}
    response_body = result
    if isinstance(result, dict):
        status = int(result.get("statusCode", result.get("status", 200)))
        response_headers = result.get("headers") or {}
        if "body" in result: response_body = result["body"]
    # A header value may be a list, one line per element (ADR-999).
    headers_out = {str(k): ([str(x) for x in v] if isinstance(v, (list, tuple)) else str(v))
                   for k, v in response_headers.items()}
    return status, headers_out, response_body

def _whole(status, headers_out, response_body):
    if response_body is None: response_body = ""
    if not isinstance(response_body, (str, bytes, bytearray)):
        response_body = json.dumps(response_body)
    if isinstance(response_body, str): response_body = response_body.encode("utf-8")
    return {
        "status": status,
        "headers": headers_out,
        "body_b64": base64.b64encode(response_body).decode("ascii"),
    }

def _error_message(exc):
    # Python slices str by code point, so this never splits a character.
    detail = str(exc)
    return detail if len(detail) <= 256 else detail[:256] + "\u2026"

def _error_envelope(message, invocation_id):
    error_body = json.dumps({"error": "handler_error", "message": message, "invocation_id": invocation_id})
    return {
        "status": 500,
        "headers": {"content-type": "application/json; charset=utf-8"},
        "body_b64": base64.b64encode(error_body.encode("utf-8")).decode("ascii"),
    }

def _serve_lines():
    for line in sys.stdin:
        if not line.strip():
            continue
        invocation_id = ""
        streaming = False
        # One failing request must not end the persistent worker. An uncaught
        # exception used to escape this loop: the worker died, the runner
        # answered a plain-text 500 that the platform treats as a retryable
        # infrastructure failure, and the durable queue re-ran the failing
        # handler. Mirror the Node adapter: a terminal handler_error envelope.
        try:
            request = json.loads(line)
            event, invocation_id = _event(request)
            result = handler(event, _Context(invocation_id, lifecycle_hook=request.get("lifecycle_hook", "")))
            if inspect.isawaitable(result): result = loop.run_until_complete(result)
            status, headers_out, response_body = _shape(result)
            if _is_stream_body(response_body):
                streaming = True
                _emit({"frame": "head", "status": status, "headers": headers_out})
                if hasattr(response_body, "__aiter__"):
                    loop.run_until_complete(_drain_async(response_body))
                else:
                    for piece in response_body:
                        _emit_chunk(piece)
                _emit({"frame": "end"})
                continue
            envelope = _whole(status, headers_out, response_body)
        except Exception as exc:
            traceback.print_exc()
            message = _error_message(exc)
            if streaming:
                # The status is already sent; the error frame tells the
                # runner to abort the response.
                _emit({"frame": "error", "message": message})
                continue
            envelope = _error_envelope(message, invocation_id)
        real_stdout.write(json.dumps(envelope) + "\n")
        real_stdout.flush()

# Handler threads and the event loop share stdout; one line at a time.
_stdout_lock = threading.Lock()

def _emit_tagged(frame):
    line = json.dumps(frame, separators=(",", ":")) + "\n"
    with _stdout_lock:
        real_stdout.write(line)
        real_stdout.flush()

async def _serve_one(request, rid, cancelled):
    # The id goes first so the runner routes the line without decoding it.
    def emit(frame):
        if not cancelled.is_set():
            _emit_tagged({"__faas_id": rid, **frame})
    invocation_id = ""
    streaming = False
    try:
        event, invocation_id = _event(request)
        status, headers_out, response_body = _shape(await handler(event, _Context(invocation_id, cancelled, request.get("lifecycle_hook", ""))))
        if _is_stream_body(response_body):
            streaming = True
            emit({"frame": "head", "status": status, "headers": headers_out})
            if hasattr(response_body, "__aiter__"):
                await _drain_async(response_body, emit)
            else:
                for piece in response_body:
                    _emit_chunk(piece, emit)
            emit({"frame": "end"})
            return
        emit(_whole(status, headers_out, response_body))
    except Exception as exc:
        traceback.print_exc()
        message = _error_message(exc)
        emit({"frame": "error", "message": message} if streaming else _error_envelope(message, invocation_id))

async def _await(awaitable):
    return await awaitable

def _serve_sync(request, rid, cancelled):
    # Runs on a handler thread. Python cannot interrupt a thread, so a
    # cancelled request's frames are dropped and a streamed body stops at
    # its next piece; the request ends when the handler returns.
    def emit(frame):
        if not cancelled.is_set():
            _emit_tagged({"__faas_id": rid, **frame})
    invocation_id = ""
    streaming = False
    try:
        event, invocation_id = _event(request)
        result = handler(event, _Context(invocation_id, cancelled, request.get("lifecycle_hook", "")))
        if inspect.isawaitable(result):
            result = asyncio.run_coroutine_threadsafe(_await(result), loop).result()
        status, headers_out, response_body = _shape(result)
        if _is_stream_body(response_body):
            streaming = True
            emit({"frame": "head", "status": status, "headers": headers_out})
            if hasattr(response_body, "__aiter__"):
                asyncio.run_coroutine_threadsafe(_drain_async(response_body, emit), loop).result()
            else:
                for piece in response_body:
                    if cancelled.is_set():
                        break
                    _emit_chunk(piece, emit)
            emit({"frame": "end"})
            return
        emit(_whole(status, headers_out, response_body))
    except Exception as exc:
        traceback.print_exc()
        message = _error_message(exc)
        emit({"frame": "error", "message": message} if streaming else _error_envelope(message, invocation_id))

_threads = None

def _start_threaded(request, rid, tasks):
    cancelled = threading.Event()
    work = loop.run_in_executor(_threads, _serve_sync, request, rid, cancelled)
    work.add_done_callback(lambda _done: tasks.pop(rid, None))
    return work, cancelled

def _cancel_threaded(rid, entry):
    # Acknowledged only once the handler's thread is free, so the runner
    # never counts a slot a running handler still holds. A handler that
    # never returns misses the acknowledgement deadline and the runner
    # restarts the worker.
    work, cancelled = entry
    cancelled.set()
    work.add_done_callback(lambda _done: _emit_tagged({"__faas_cancelled": rid}))

# Ack cancellation only once async finally blocks or handler threads have
# completed; a running cleanup still owns its worker slot.
async def _serve_shared():
    reader = asyncio.StreamReader(limit=1 << 30)
    await loop.connect_read_pipe(lambda: asyncio.StreamReaderProtocol(reader), sys.stdin)
    tasks = {}
    while True:
        line = await reader.readline()
        if not line:
            return
        if not line.strip():
            continue
        try:
            request = json.loads(line)
        except ValueError:
            traceback.print_exc()
            continue
        cancel = request.get("__faas_cancel")
        if cancel is not None:
            entry = tasks.get(cancel)
            if entry is not None and entry[1].is_set():
                continue
            if entry is not None and not handler_is_async:
                _cancel_threaded(cancel, entry)
                continue
            if entry is not None:
                work, cancelled = entry
                cancelled.set()
                work.cancel()
                work.add_done_callback(lambda _done, rid=cancel: _emit_tagged({"__faas_cancelled": rid}))
            else:
                _emit_tagged({"__faas_cancelled": cancel})
            continue
        rid = str(request.get("__faas_id", ""))
        if handler_is_async:
            cancelled = threading.Event()
            work = loop.create_task(_serve_one(request, rid, cancelled))
            work.add_done_callback(lambda _done, rid=rid: tasks.pop(rid, None))
            tasks[rid] = (work, cancelled)
        else:
            tasks[rid] = _start_threaded(request, rid, tasks)

if concurrency > 1:
    if not handler_is_async:
        _threads = concurrent.futures.ThreadPoolExecutor(max_workers=concurrency, thread_name_prefix="faas-handler")
    loop.run_until_complete(_serve_shared())
else:
    _serve_lines()
`

const (
	nodeAdapterHandlerImport   = `const handlerFile = path.join(path.dirname(process.argv[1]), "handler.js");`
	pythonAdapterHandlerImport = `handler_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".faas-handler.py")`
)

// nodeFunctionAdapterFor returns the Node adapter importing handlerRel, a
// slash path relative to the adapter's directory. The default handler.js
// yields the historical adapter byte for byte.
func nodeFunctionAdapterFor(handlerRel string) string {
	if handlerRel == "handler.js" {
		return nodeFunctionAdapter
	}
	return strings.Replace(nodeFunctionAdapter, nodeAdapterHandlerImport,
		"const handlerFile = path.join(path.dirname(process.argv[1]), "+strconv.Quote(handlerRel)+");", 1)
}

// pythonFunctionAdapterFor returns the Python adapter loading handlerRel in
// place and putting its directory on sys.path, so sibling imports in a
// shared source tree keep working.
func pythonFunctionAdapterFor(handlerRel string) string {
	return strings.Replace(pythonFunctionAdapter, pythonAdapterHandlerImport,
		"handler_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "+strconv.Quote(handlerRel)+")\n"+
			"sys.path.insert(0, os.path.dirname(handler_path))", 1)
}

func wrapPythonFunctionHandler(target string, source []byte) error {
	impl := filepath.Join(filepath.Dir(target), ".faas-handler.py")
	if _, err := os.Stat(impl); err == nil {
		return fmt.Errorf("rootfs: python function implementation %q already exists", impl)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rootfs: stat python function implementation %q: %w", impl, err)
	}
	if err := os.Rename(target, impl); err != nil {
		return fmt.Errorf("rootfs: preserve python function source: %w", err)
	}
	if err := os.WriteFile(target, []byte(pythonFunctionAdapter), 0o644); err != nil {
		_ = os.Rename(impl, target)
		return fmt.Errorf("rootfs: write python function adapter %s: %w", target, err)
	}
	return nil
}

// InjectFunctionRunner copies the function runner binary at
// /usr/local/bin/faas-runner so guest-init can exec it (spec §4.9).
// Empty path = no-op (image deploys don't need it).
func InjectFunctionRunner(staging, runnerPath string) error {
	_, err := injectFunctionRunner(staging, runnerPath)
	return err
}

// injectFunctionRunner copies the runner and returns the digest of the exact
// bytes written. Keeping hashing beside the copy prevents provenance from
// drifting if the source file changes between a separate hash and injection.
func injectFunctionRunner(staging, runnerPath string) (string, error) {
	data, err := os.ReadFile(runnerPath)
	if err != nil {
		return "", fmt.Errorf("rootfs: read function runner: %w", err)
	}
	sum := sha256.Sum256(data)
	dst := filepath.Join(staging, "usr", "local", "bin", "faas-runner")
	if err := mkdirImageDirectories(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("rootfs: open function runner: %w", err)
	}
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Chmod(0o755), f.Close()); err != nil {
		return "", fmt.Errorf("rootfs: write function runner: %w", err)
	}
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// limitsFor resolves the plan limits a build enforces (app-layer cap,
// tarball cap). Package-level so tests can shrink the cap to a few MiB and
// exercise the violation paths without writing hundreds of megabytes to
// disk; production always resolves through api.LimitsFor.
var limitsFor = api.LimitsFor
