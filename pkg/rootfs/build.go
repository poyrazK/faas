package rootfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
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

// Builder assembles app layers.
type Builder struct {
	run Runner
}

// NewBuilder wires a Builder with the command runner used for mkfs.
func NewBuilder(run Runner) *Builder {
	return &Builder{run: run}
}

// BuildInput is one app-layer build.
type BuildInput struct {
	// Layers are the above-base OCI layers, bottom-to-top, gzip-compressed.
	Layers []io.Reader
	// Manifest is the /etc/faas/app.json contract to inject.
	Manifest api.AppManifest
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
	// FunctionRunnerPath, when set, is copied into the layer at
	// /usr/local/bin/faas-runner so the guest can exec it. Wired from
	// cmd/imaged's config; empty skips the runner injection.
	FunctionRunnerPath string
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
		if err := ApplyTarball(staging, in.TarballPath); err != nil {
			return BuildResult{}, fmt.Errorf("rootfs: apply function tarball: %w", err)
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

	content, err := DirSize(staging)
	if err != nil {
		return BuildResult{}, err
	}
	sizeMB, err := CheckCap(limits, content)
	if err != nil {
		return BuildResult{}, err // *api.Problem naming cap + observed size
	}

	if err := b.publishExt4(ctx, in, staging, sizeMB); err != nil {
		return BuildResult{}, err
	}

	res := BuildResult{SizeMB: sizeMB, ContentBytes: content}
	if in.OutImage != "" {
		res.ImagePath = in.OutImage
	} else {
		res.ImageKey = in.StorageKey
	}
	return res, nil
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
	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("rootfs: open mkfs output: %w", err)
	}
	closed = true // release the open file before Put; storage Put closes the file via defer elsewhere
	defer func() { _ = f.Close() }()
	if err := in.Storage.Put(ctx, in.StorageKey, f); err != nil {
		return fmt.Errorf("rootfs: publish %q: %w", in.StorageKey, err)
	}
	return nil
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
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("rootfs: write guest-init: %w", err)
	}
	return nil
}

// ApplyTarball unpacks a customer source tarball at /app. Used by the
// function-deploy path; the tarball is the customer's handler code
// (handler.js / handler.py). Path-escape protection reuses the
// ApplyLayerGz allowlist so a malicious tarball can't escape /app.
//
//nolint:forbidigo // tarballPath is the apid-spooled path under spoolRoot() that already passed apid's validateTarballShape (in cmd/apid/deploy_inputs.go) — bytes are validated before builderd opens them; symlink-attack on the open itself is impossible because apid wrote the file via os.Create above with a fresh random id. The "customer" framing in the doc comment refers to the *contents* of the tarball (handler code), not the file path on disk.
func ApplyTarball(staging, tarballPath string) error {
	f, err := os.Open(tarballPath)
	if err != nil {
		return fmt.Errorf("rootfs: open tarball: %w", err)
	}
	defer func() { _ = f.Close() }()
	appDir := filepath.Join(staging, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}
	return ApplyLayerGz(appDir, f)
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
