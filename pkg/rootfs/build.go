package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	// Layers are the above-base OCI layers, bottom-to-top, gzip-compressed.
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
}

// BuildResult reports the produced layer.
type BuildResult struct {
	// ImageKey is the storage key the ext4 was published under, when
	// Storage was used. Empty when the legacy OutImage path produced
	// the file.
	ImageKey string
	// ImagePath is the on-disk path the ext4 was written to, when
	// OutImage was used. Empty when Storage published the file.
	ImagePath    string
	SizeMB       int
	ContentBytes int64
	// SBOMKey is the storage key the CycloneDX SBOM was published
	// under (issue #299 / ADR-038 Phase 3). Empty when the build
	// did not configure SBOMRun + SBOMStorageKey, or when the
	// emission failed (best-effort: the build still succeeds).
	SBOMKey string
}

// Build runs the pipeline. It stages into a temp dir that is always removed.
func (b *Builder) Build(ctx context.Context, in BuildInput) (BuildResult, error) {
	limits, ok := api.LimitsFor(in.Plan)
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

	for i, layer := range in.Layers {
		if err := ApplyLayerGz(staging, layer); err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: apply layer %d: %w", i, err)
		}
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
	if in.FunctionRunnerPath != "" {
		if err := InjectFunctionRunner(staging, in.FunctionRunnerPath); err != nil {
			return BuildResult{}, err
		}
	}
	if err := InjectGuestInit(staging, in.GuestInitPath); err != nil {
		return BuildResult{}, err
	}
	if err := InjectManifest(staging, in.Manifest); err != nil {
		return BuildResult{}, err
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
	}

	stats, err := InspectStaging(staging)
	if err != nil {
		return BuildResult{}, err
	}
	sizeMB, err := CheckCapForStaging(limits, stats)
	if err != nil {
		return BuildResult{}, err // *api.Problem naming cap + observed size
	}

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
	if err := stageAppUpper(staging); err != nil {
		return BuildResult{}, err
	}

	if err := b.publishExt4(ctx, in, staging, sizeMB); err != nil {
		return BuildResult{}, err
	}

	res := BuildResult{SizeMB: sizeMB, ContentBytes: stats.ContentBytes, SBOMKey: sbomKey}
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
func stageAppUpper(staging string) error {
	upper := filepath.Join(staging, "upper")
	legacyUpper := filepath.Join(staging, ".faas-app-upper-source")
	if _, err := os.Lstat(upper); err == nil {
		if err := os.Rename(upper, legacyUpper); err != nil {
			return fmt.Errorf("rootfs: preserve app upper path: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rootfs: inspect app upper path: %w", err)
	}
	if err := os.Mkdir(upper, 0o755); err != nil {
		return fmt.Errorf("rootfs: create app upper path: %w", err)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return fmt.Errorf("rootfs: read app staging: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == "upper" {
			continue
		}
		src := filepath.Join(staging, entry.Name())
		dst := filepath.Join(upper, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("rootfs: move %s into app upper: %w", entry.Name(), err)
		}
	}
	return nil
}

// publishExt4 mkfs-es the staging tree into a temp file (or directly into
// OutImage in the legacy path) and then either renames into OutImage or
// streams into the storage backend. The tmp-file indirection keeps mkfs
// happy (it insists on a path, not stdout) and lets us atomically publish
// to the storage backend via Put.
//
// The temp file is removed before returning; the caller sees no scratch
// left behind even on error.
func (b *Builder) publishExt4(ctx context.Context, in BuildInput, staging string, sizeMB int) error {
	if in.OutImage != "" {
		// Legacy path. Mkfs writes directly to OutImage; the caller's
		// filesystem already provides atomicity (or it doesn't, and we
		// honour that — pre-#96 production). Kept for the integration
		// test.
		if err := os.MkdirAll(filepath.Dir(in.OutImage), 0o755); err != nil {
			return fmt.Errorf("rootfs: mkdir out dir: %w", err)
		}
		if err := b.run.Run(ctx, MkfsCommand(staging, in.OutImage, sizeMB)); err != nil {
			return fmt.Errorf("rootfs: mkfs: %w", err)
		}
		return nil
	}
	// Storage path. Mkfs into a sibling temp file, then Put the bytes
	// under StorageKey and remove the temp.
	tmp, err := os.CreateTemp(filepath.Dir(staging), "faas-mkfs-*.ext4")
	if err != nil {
		return fmt.Errorf("rootfs: create tmp ext4: %w", err)
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
		return fmt.Errorf("rootfs: close tmp ext4: %w", err)
	}
	if err := b.run.Run(ctx, MkfsCommand(staging, tmpPath, sizeMB)); err != nil {
		return fmt.Errorf("rootfs: mkfs: %w", err)
	}
	// nolint:forbidigo // tmpPath is from os.MkdirTemp at the top of
	// this function — a daemon-internal scratch file the builder just
	// wrote via MkfsCommand. Not a customer path.
	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("rootfs: open mkfs output: %w", err)
	}
	closed = true // release the open file before Put; storage Put closes the file via defer elsewhere
	defer func() { _ = f.Close() }()
	if err := in.Storage.Put(ctx, in.StorageKey, f); err != nil {
		return fmt.Errorf("rootfs: publish %q: %w", in.StorageKey, err)
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
			return fmt.Errorf("rootfs: sign %q: %w", in.StorageKey, err)
		}
	}
	return nil
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
	if guestInitPath == "" {
		return fmt.Errorf("rootfs: empty guest-init path")
	}
	data, err := os.ReadFile(guestInitPath)
	if err != nil {
		return fmt.Errorf("rootfs: read guest-init: %w", err)
	}
	dst := filepath.Join(staging, "sbin", "init")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// OCI base images commonly ship /sbin/init as a symlink (for example
	// Alpine points it at /bin/busybox). Remove the link before writing the
	// platform PID-1 binary; os.WriteFile follows a symlink and would
	// overwrite the link target while leaving /sbin/init pointing at it.
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rootfs: remove existing init: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("rootfs: write guest-init: %w", err)
	}
	return nil
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
		archiveName := hdr.Name
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
			if err := applyEntry(dst, target, hdr, tr); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("rootfs: archive entry %q contains traversal marker", hdr.Name)
	}
}

// NormalizeFunctionHandler makes the source filename agree with the runtime
// runner's manifest path. The public function contract asks customers for an
// exported handler(event, ctx), while the low-level runner consumes the
// §4.9 stdin/stdout envelope. For exported Node/Python handlers, generate a
// small protocol adapter at the manifest path and keep the customer's source
// filename intact. Protocol-style handlers remain supported for compatibility
// and are still aliased exactly as before.
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
	if filepath.Ext(target) == ".js" && isNodeFunctionSource(string(data)) {
		if err := os.WriteFile(target, []byte(nodeFunctionAdapter), 0o644); err != nil {
			return fmt.Errorf("rootfs: write node function adapter %s: %w", target, err)
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
	return strings.Contains(source, "export") && strings.Contains(source, "handler")
}

func isPythonFunctionSource(source string) bool {
	return strings.Contains(source, "def handler(") || strings.Contains(source, "async def handler(")
}

// nodeFunctionAdapter translates the public handler(event, ctx) contract to
// the runner's protocol envelope. It lives in the app layer so the runner can
// remain a deliberately tiny, protocol-only binary shared by all Node apps.
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
const fn = mod.handler || mod.default;
if (typeof fn !== "function") throw new Error("handler.js must export handler or default");
if (process.env.FAAS_PERSISTENT_WORKER === "1") {
  process.stdout.write(JSON.stringify({ __faas_ready: true }) + "\n");
}

const lines = readlineModule.createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const line of lines) {
  if (!line.trim()) continue;
  const env = JSON.parse(line);
  const raw = Buffer.from(env.body_b64 || "", "base64").toString("utf8");
  let body = raw;
  if (raw === "") {
    body = null;
  } else {
    try { body = JSON.parse(raw); } catch (_) {}
  }
  const headers = env.headers || {};
  const invocationID = headers["x-faas-invocation-id"] || headers["X-Faas-Invocation-Id"] || "";
  const log = {};
  for (const level of ["debug", "info", "warn", "error"]) {
    log[level] = (...args) => console.error(...args);
  }
  const ctx = { invocation_id: invocationID, log };
  const event = {
    method: env.method || "POST",
    path: env.path || "/",
    headers,
    query: env.query || "",
    body,
  };
  const value = await fn(event, ctx);
  let status = 200;
  let responseHeaders = {};
  let responseBody = value;
  if (value && typeof value === "object" && !Buffer.isBuffer(value)) {
    status = Number(value.statusCode ?? value.status ?? 200);
    responseHeaders = value.headers || {};
    if (Object.prototype.hasOwnProperty.call(value, "body")) responseBody = value.body;
  }
  if (responseBody === undefined || responseBody === null) responseBody = "";
  if (typeof responseBody !== "string" && !Buffer.isBuffer(responseBody)) {
    responseBody = JSON.stringify(responseBody);
  }
  const normalizedHeaders = {};
  for (const [key, value] of Object.entries(responseHeaders)) normalizedHeaders[key] = String(value);
  process.stdout.write(JSON.stringify({
    status,
    headers: normalizedHeaders,
    body_b64: Buffer.from(responseBody).toString("base64"),
  }) + "\n");
}
})().catch((err) => {
  console.error(err && err.stack ? err.stack : err);
  process.exitCode = 1;
});
`

const pythonFunctionAdapter = `# FAAS_PERSISTENT_PROTOCOL_V1
import asyncio
import base64
import importlib.util
import inspect
import json
import os
import sys

class _Log:
    def info(self, *args, **kwargs): print(*args, file=sys.stderr)
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
if os.environ.get("FAAS_PERSISTENT_WORKER") == "1":
    real_stdout.write(json.dumps({"__faas_ready": True}) + "\n")
    real_stdout.flush()

for line in sys.stdin:
    if not line.strip():
        continue
    env = json.loads(line)
    raw = base64.b64decode(env.get("body_b64", "")).decode("utf-8")
    if raw == "":
        body = None
    else:
        try:
            body = json.loads(raw)
        except json.JSONDecodeError:
            body = raw
    headers = env.get("headers") or {}
    invocation_id = headers.get("x-faas-invocation-id", headers.get("X-Faas-Invocation-Id", ""))

    class _Context:
        log = _Log()
        def __init__(self, invocation): self.invocation_id = invocation

    event = {
        "method": env.get("method") or "POST",
        "path": env.get("path") or "/",
        "headers": headers,
        "query": env.get("query") or "",
        "body": body,
    }
    result = handler(event, _Context(invocation_id))
    if inspect.isawaitable(result): result = asyncio.run(result)

    status = 200
    response_headers = {}
    response_body = result
    if isinstance(result, dict):
        status = int(result.get("statusCode", result.get("status", 200)))
        response_headers = result.get("headers") or {}
        if "body" in result: response_body = result["body"]
    if response_body is None: response_body = ""
    if not isinstance(response_body, (str, bytes, bytearray)):
        response_body = json.dumps(response_body)
    if isinstance(response_body, str): response_body = response_body.encode("utf-8")
    real_stdout.write(json.dumps({
        "status": status,
        "headers": {str(k): str(v) for k, v in response_headers.items()},
        "body_b64": base64.b64encode(response_body).decode("ascii"),
    }) + "\n")
    real_stdout.flush()
`

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
	data, err := os.ReadFile(runnerPath)
	if err != nil {
		return fmt.Errorf("rootfs: read function runner: %w", err)
	}
	dst := filepath.Join(staging, "usr", "local", "bin", "faas-runner")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("rootfs: write function runner: %w", err)
	}
	return nil
}
