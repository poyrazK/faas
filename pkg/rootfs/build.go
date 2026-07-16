package rootfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// Builder turns the OCI layers that sit above a base image into a bootable per-app
// ext4 layer (drive1, spec §4.6): apply layers into a staging tree, inject
// guest-init as /sbin/init and the app.json contract, enforce the plan cap, then
// mkfs from the staging directory. Everything except the final unprivileged mkfs
// runs in pure Go and is unit-tested.

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
	// OutImage is where layer.ext4 is written.
	OutImage string
}

// BuildResult reports the produced layer.
type BuildResult struct {
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

	if err := b.run.Run(ctx, MkfsCommand(staging, in.OutImage, sizeMB)); err != nil {
		return BuildResult{}, fmt.Errorf("rootfs: mkfs: %w", err)
	}
	return BuildResult{ImagePath: in.OutImage, SizeMB: sizeMB, ContentBytes: content}, nil
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
