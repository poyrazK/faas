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
	if dep.BuildID == "" || app.Type == state.AppTypeFunction || app.Runtime != "" {
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

// sharedRuntimeBaseDiffIDs returns the layer diff IDs of the runner base
// dep's build artifact was made FROM, so the artifact splits at that base.
// builderd records the digest-pinned base in the build's provenance. A
// release that moves the node's runner-base pin while a build is in flight
// leaves an artifact made FROM the previous base (release N runs beside
// N-1, docs/release-compatibility.md), and splitting it at the new pin
// failed the deployment with image_manifest_invalid. Without a recorded
// base for runtime, the node's current base is used. drive0 stays the
// node's current runner base either way (ADR-1500 §4): the /app layer
// holds no Node, and the base keeps the build's major.
func (h *Handler) sharedRuntimeBaseDiffIDs(ctx context.Context, runtime string, dep state.Deployment) ([]string, error) {
	builtFrom := ""
	if dep.BuildID != "" {
		prov, err := h.store.BuildProvenanceByBuildID(ctx, dep.BuildID)
		switch {
		case errors.Is(err, state.ErrNotFound):
		case err != nil:
			return nil, fmt.Errorf("read build provenance: %w", err)
		case recordedRunnerBase(prov.RuntimeBaseRef, runtime):
			builtFrom = prov.RuntimeBaseRef
		}
	}
	_, ids, err := h.runtimeBaseDiffIDs(ctx, runtime, builtFrom)
	return ids, err
}

// recordedRunnerBase reports whether ref is a digest-pinned runner base for
// runtime, the only recorded base an artifact may be split at.
func recordedRunnerBase(ref, runtime string) bool {
	parsed, err := oci.ParseReference(ref)
	return err == nil && parsed.Digest != "" && runtime != "" && runtimeFromBaseRef(ref) == runtime
}

type sharedRuntimeLayers struct {
	layers  []io.Reader
	cleanup func()
	// Reason names why the layer keeps everything above the base; empty
	// when it holds only /app.
	Reason string
	// OutsideApp names the first files outside /app that are not toolchain
	// when Reason is content_outside_app.
	OutsideApp []string
}

// sharedRuntimeAppLayers splits a build artifact at its runner base and
// rewrites manifest for an /app-only layer when the artifact qualifies.
// archivePath reopens the artifact for the full above-base fallback.
func (h *Handler) sharedRuntimeAppLayers(ctx context.Context, runtime, archivePath string, config oci.Config, layers []io.ReadCloser, manifest *api.AppManifest, app state.App) (sharedRuntimeLayers, error) {
	_, baseDiffIDs, err := h.runtimeBaseDiffIDs(ctx, runtime, "")
	if err != nil {
		return sharedRuntimeLayers{}, err
	}
	return h.sharedRuntimeAppLayersAt(ctx, runtime, baseDiffIDs, archivePath, config, layers, manifest, app, sharedRuntimeSplit{})
}

// sharedRuntimeSplit is what the deployment and its plan need from the
// shared-runtime split.
type sharedRuntimeSplit struct {
	// keepsToolchain keeps every layer above the base: the release command
	// calls a package manager the runner base lacks.
	keepsToolchain bool
	// keepsApp keeps all of /app: a release command may read sources,
	// scripts or node_modules a Next.js standalone tree leaves out
	// (ADR-20261124160000).
	keepsApp bool
}

// sharedRuntimeAppLayersAt is sharedRuntimeAppLayers at the runner base
// layers baseDiffIDs. The result follows from the artifact, baseDiffIDs,
// runtime, manifest, app.StartCommand and release, so the app layer reuse
// key names those instead of the result (ADR-20261112170911 amendment 1).
func (h *Handler) sharedRuntimeAppLayersAt(ctx context.Context, runtime string, baseDiffIDs []string, archivePath string, config oci.Config, layers []io.ReadCloser, manifest *api.AppManifest, app state.App, release sharedRuntimeSplit) (sharedRuntimeLayers, error) {
	releaseKeepsToolchain := release.keepsToolchain
	above, err := oci.LayersAboveBase(baseDiffIDs, config.DiffIDs)
	if err != nil {
		return sharedRuntimeLayers{}, fmt.Errorf("runtime base %s does not match the build artifact: %w", runtime, err)
	}
	start := len(config.DiffIDs) - len(above)
	if start < 0 || start > len(layers) {
		return sharedRuntimeLayers{}, fmt.Errorf("runtime base layer boundary %d is outside artifact layer count %d", start, len(layers))
	}
	if releaseKeepsToolchain {
		return sharedRuntimeLayers{layers: layersAsReaders(layers[start:]), cleanup: func() {}, Reason: sharedruntime.ReasonReleasePackageManager}, nil
	}
	report := &appLayerReport{PythonInterpreter: sharedruntime.PythonInterpreter(runtime)}
	nodeRunner := !sharedruntime.PythonRuntime(runtime)
	command := sharedRuntimeCommand(*manifest, app)
	report.KeepBun = nodeRunner && sharedruntime.UsesBun(command)
	filtered, cleanup, filterErr := filterAppLayers(layers[start:], report)
	reason := ""
	switch {
	case filterErr != nil:
		reason = "no_app_files"
	case len(report.Unexpected) > 0:
		reason = "content_outside_app"
	default:
		nextArgv, nextStart := sharedruntime.NextStandaloneStartFor(command, report.PackageJSON)
		if nodeRunner && report.NextStandalone && !release.keepsApp && nextStart {
			// ADR-20261124160000: the builder kept a standalone tree, which
			// it does only when the whole /app is over the plan's app layer
			// cap. Ship only that tree.
			next, nextCleanup, err := selectNextStandalone(filtered)
			if err == nil {
				cleanup()
				setSharedRuntimeStart(manifest, nextArgv, sharedruntime.RunnerPath)
				for k, v := range sharedruntime.NextStandaloneEnvVars {
					if _, set := manifest.Env[k]; !set {
						manifest.Env[k] = v
					}
				}
				return sharedRuntimeLayers{layers: next, cleanup: nextCleanup}, nil
			}
			h.log.Info("imaged: Next.js standalone selection failed; keeping the layer as built", "app", app.Slug, "err", err)
			if err := rewindLayers(filtered); err != nil {
				reason = sharedruntime.ReasonNextStandaloneReread
				break
			}
		}
		if report.KeepBun {
			// ADR-20261124160000: Bun runs its own scripts; the layer keeps
			// Bun's install and the runner base supplies Node.
			if report.BunBinDir == "" {
				reason = sharedruntime.ReasonBunNotInstalled
				break
			}
			if sharedruntime.BunStartNeedsNodePackageManager(command, report.PackageJSON) {
				// npm/npx/yarn/pnpm came with mise's Node, which the
				// layer would drop.
				reason = sharedruntime.ReasonPackageManagerStart
				break
			}
			argv := sharedruntime.ShellStart(command)
			if strings.TrimSpace(app.StartCommand) == "" && !shellEntrypoint(manifest.Entrypoint) {
				// Exec form keeps its words as the image gave them.
				argv = manifest.Entrypoint
			}
			setSharedRuntimeStart(manifest, argv, sharedruntime.BunRunnerPath(report.BunBinDir))
			return sharedRuntimeLayers{layers: filtered, cleanup: cleanup}, nil
		}
		if nodeRunner && sharedruntime.StartReachesBun(command, report.PackageJSON) {
			// A package manager script runs Bun; keep the toolchain.
			reason = sharedruntime.ReasonBunInScript
			break
		}
		if nodeRunner && report.YarnPnP {
			// ADR-20261125170000: only yarn's loader resolves a Plug'n'Play
			// install, and the runner base has no yarn.
			reason = sharedruntime.ReasonYarnPnP
			break
		}
		argv, why := sharedRuntimeStart(*manifest, app, runtime, report)
		if why != "" {
			reason = why
			break
		}
		setSharedRuntimeStart(manifest, argv, sharedruntime.RunnerPath)
		if report.PythonInterpreter != "" {
			manifest.Env["PATH"] = sharedruntime.PythonRunnerPath
			manifest.Env["VIRTUAL_ENV"] = "/app/.venv"
		}
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
	return sharedRuntimeLayers{layers: layersAsReaders(all[start:]), cleanup: allCleanup, Reason: reason, OutsideApp: report.Unexpected}, nil
}

// setSharedRuntimeStart points manifest at argv with PATH path.
func setSharedRuntimeStart(manifest *api.AppManifest, argv []string, path string) {
	manifest.Entrypoint = argv
	if manifest.Env == nil {
		manifest.Env = map[string]string{}
	}
	manifest.Env["PATH"] = path
}

// sharedRuntimeCommand is the app's start command as written: the
// customer's start command when set, else the image's shell-form command or
// its words joined.
func sharedRuntimeCommand(manifest api.AppManifest, app state.App) string {
	if command := strings.TrimSpace(app.StartCommand); command != "" {
		return command
	}
	argv := manifest.Entrypoint
	if shellEntrypoint(argv) {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

// shellEntrypoint reports whether argv is a shell-form `sh -c <command>`.
func shellEntrypoint(argv []string) bool {
	return len(argv) == 3 && (argv[0] == "/bin/sh" || argv[0] == "/bin/bash" || argv[0] == "sh" || argv[0] == "bash") && argv[1] == "-c"
}

// sharedRuntimeStart resolves the app's start command for the runner base,
// which ships no npm and no Python package manager: the customer's start
// command when set, else the image's shell-form command. report is what the
// app layer held.
func sharedRuntimeStart(manifest api.AppManifest, app state.App, runtime string, report *appLayerReport) ([]string, string) {
	resolve := func(command string) ([]string, string) {
		return sharedruntime.ResolveStart(command, report.PackageJSON)
	}
	// An exec-form start through a package manager resolves like the shell
	// form: npm, pnpm and yarn run a script (ADR-20261125170000), and the
	// others are not on the runner base.
	needsTool := func(name string) bool {
		switch name {
		case "npm", "pnpm", "yarn", "npx", "corepack":
			return true
		}
		return false
	}
	if sharedruntime.PythonRuntime(runtime) {
		resolve = func(command string) ([]string, string) {
			return sharedruntime.ResolvePythonStart(command, &report.Python)
		}
		needsTool = sharedruntime.PythonPackageManager
	}
	command := strings.TrimSpace(app.StartCommand)
	if command == "" {
		argv := manifest.Entrypoint
		switch {
		case len(argv) == 3 && (argv[0] == "/bin/sh" || argv[0] == "/bin/bash" || argv[0] == "sh" || argv[0] == "bash") && argv[1] == "-c":
			command = argv[2]
		case len(argv) > 0 && !needsTool(argv[0]):
			return argv, ""
		default:
			command = strings.Join(argv, " ")
		}
	}
	return resolve(command)
}
