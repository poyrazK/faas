package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
	"github.com/onebox-faas/faas/pkg/state"
)

// A source-built container app whose signed export claims a shared runner
// base (docs/adr/20261017-shared-runtime-source-apps.md, H10-140) was built
// FROM that base. Its layer is everything above the base; when the only
// content outside /app is the mise toolchain and caches the base replaces,
// and the start command resolves without npm, the layer keeps only /app.

// sharedRuntimeClaim returns the runner base a source build's approved
// export claims for app, or "" for the app's own base.
func sharedRuntimeClaim(app state.App, source *sourceBuildRootfsBinding) string {
	if source == nil {
		return ""
	}
	claimed := source.Approval.Input.Claims.Runtime
	if claimed == app.Runtime || !state.SourceRuntimeClaimAllowed(app, claimed) {
		return ""
	}
	return claimed
}

// sharedRuntimeFromProvenance returns the runner base an unsigned source build
// of app was made FROM, read from the build's provenance (builderd records
// the base it built on). Only a node without a build publisher takes this
// path; a signed build's runner base comes from its export claim.
func (h *Handler) sharedRuntimeFromProvenance(ctx context.Context, app state.App, dep state.Deployment) (string, error) {
	// builderd never builds a job on a runner base. When the minimal base is
	// itself a runner image (the e2e override collapses every base onto one
	// ref), its provenance must not turn the job's layer into a runner layer.
	if dep.BuildID == "" || !state.SharedSourceAppCandidate(app) {
		return "", nil
	}
	prov, err := h.store.BuildProvenanceByBuildID(ctx, dep.BuildID)
	if errors.Is(err, state.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		// Guessing the app's own base for an artifact built FROM a runner
		// base would boot the wrong drive0.
		return "", fmt.Errorf("read build provenance: %w", err)
	}
	runtime := runtimeFromBaseRef(prov.RuntimeBaseRef)
	if !state.SharedSourceAppRuntime(runtime) || !state.SourceRuntimeClaimAllowed(app, runtime) {
		return "", nil
	}
	return runtime, nil
}

// runtimeFromBaseRef maps a runner base reference such as
// ghcr.io/poyrazk/runner-node24@sha256:… to its runtime ("node24").
func runtimeFromBaseRef(ref string) string {
	parsed, err := oci.ParseReference(ref)
	if err != nil {
		return ""
	}
	name := parsed.Repository
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	runtime, ok := strings.CutPrefix(name, "runner-")
	if !ok {
		return ""
	}
	return runtime
}

type sharedRuntimeLayers struct {
	layers  []io.Reader
	cleanup func()
	// Reason names why the layer keeps everything above the base; empty
	// when it holds only /app.
	Reason string
}

// sharedRuntimeAppLayers splits a build artifact at its runner base and
// rewrites manifest for an /app-only layer when the artifact qualifies.
// archivePath reopens the artifact for the full above-base fallback.
func (h *Handler) sharedRuntimeAppLayers(ctx context.Context, runtime, archivePath string, config oci.Config, layers []io.ReadCloser, manifest *api.AppManifest, app state.App) (sharedRuntimeLayers, error) {
	_, baseDiffIDs, err := h.runtimeBaseDiffIDs(ctx, runtime, "")
	if err != nil {
		return sharedRuntimeLayers{}, err
	}
	above, err := oci.LayersAboveBase(baseDiffIDs, config.DiffIDs)
	if err != nil {
		return sharedRuntimeLayers{}, fmt.Errorf("runtime base %s does not match the build artifact: %w", runtime, err)
	}
	start := len(config.DiffIDs) - len(above)
	if start < 0 || start > len(layers) {
		return sharedRuntimeLayers{}, fmt.Errorf("runtime base layer boundary %d is outside artifact layer count %d", start, len(layers))
	}
	report := &appLayerReport{}
	filtered, cleanup, filterErr := filterAppLayers(layers[start:], report)
	reason := ""
	switch {
	case filterErr != nil:
		reason = "no_app_files"
	case len(report.Unexpected) > 0:
		reason = "content_outside_app"
	default:
		argv, why := sharedRuntimeStart(*manifest, app, report.PackageJSON)
		if why != "" {
			reason = why
			break
		}
		manifest.Entrypoint = argv
		if manifest.Env == nil {
			manifest.Env = map[string]string{}
		}
		manifest.Env["PATH"] = sharedruntime.RunnerPath
		return sharedRuntimeLayers{layers: filtered, cleanup: cleanup}, nil
	}
	if filterErr == nil {
		cleanup()
	}
	// The filter consumed the readers; reopen the artifact and keep every
	// layer above the runner base with the image's own start command.
	_, all, allCleanup, err := loadLocalOCIArchiveContext(ctx, archivePath)
	if err != nil {
		return sharedRuntimeLayers{}, fmt.Errorf("reload build artifact: %w", err)
	}
	if start > len(all) {
		allCleanup()
		return sharedRuntimeLayers{}, fmt.Errorf("runtime base layer boundary %d is outside artifact layer count %d", start, len(all))
	}
	return sharedRuntimeLayers{layers: layersAsReaders(all[start:]), cleanup: allCleanup, Reason: reason}, nil
}

// sharedRuntimeStart resolves the app's start command for the runner base,
// which ships no npm: the customer's start command when set, else the
// image's shell-form command.
func sharedRuntimeStart(manifest api.AppManifest, app state.App, packageJSON []byte) ([]string, string) {
	command := strings.TrimSpace(app.StartCommand)
	if command == "" {
		argv := manifest.Entrypoint
		switch {
		case len(argv) == 3 && (argv[0] == "/bin/sh" || argv[0] == "/bin/bash" || argv[0] == "sh" || argv[0] == "bash") && argv[1] == "-c":
			command = argv[2]
		case len(argv) > 0 && argv[0] != "npm":
			return argv, ""
		default:
			command = strings.Join(argv, " ")
		}
	}
	return sharedruntime.ResolveStart(command, packageJSON)
}
