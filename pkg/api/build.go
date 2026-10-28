package api

import "regexp"

// BuildManifestPath is where builderd writes the manifest onto drive1 at
// CreateColdBoot time and where guest-init reads it at boot to decide
// "build mode" vs "app mode" (M6 / spec §4.5).
const BuildManifestPath = "/etc/faas/build.json"

// BuildEntropyPath is a per-build seed staged by builderd and consumed by
// guest-init before starting BuildKit. A fresh microVM can otherwise block in
// crypto/rand while BuildKit generates its proxy CA before virtio-rng has
// initialized the guest entropy pool.
const BuildEntropyPath = "/etc/faas/entropy.seed"

// BuildDonePath is where guest-init writes the build result before exiting,
// and where vmmd's Destroy copies it into <export_dir>/build-done.json before
// removing the chroot.
const BuildDonePath = "/etc/faas/build-done.json"

// IssueSourceMapBuildConfigFile is read from the selected source root, before
// customer build commands run. It opts a Node source build into publication.
const IssueSourceMapBuildConfigFile = "gregale.issue-source-maps.json"

// BuildIssueSourceMapsFile is a sanitized sidecar in the existing build export.
const BuildIssueSourceMapsFile = "issue-source-maps.json"

type IssueSourceMapBuildConfig struct {
	Version   int    `json:"version"`
	Directory string `json:"directory"`
}

type BuildIssueSourceMaps struct {
	BuildID      string                        `json:"build_id"`
	DeploymentID string                        `json:"deployment_id"`
	SourceSHA256 string                        `json:"source_sha256"`
	ImageSHA256  string                        `json:"image_sha256"`
	Request      PublishIssueSourceMapsRequest `json:"request"`
}

// BuildFramework picks which in-VM build engine the builder VM invokes.
// SchemaVersion'd for forward compat — additive enum slots only.
type BuildFramework string

const (
	// FrameworkRailpackNode uses railpack with the Node plan.
	FrameworkRailpackNode BuildFramework = "railpack_node"
	// FrameworkRailpackPython uses railpack with the Python plan.
	FrameworkRailpackPython BuildFramework = "railpack_python"
	// FrameworkRailpackGo uses railpack with the Go plan. Static-binary
	// output lands in the layer at /app/server (app mode) or /app/handler
	// (function mode, per imaged.handleDeployment). The runner shim is
	// only used for function deploys; the app path execs the binary
	// directly via AppManifest.Entrypoint.
	FrameworkRailpackGo BuildFramework = "railpack_go"
	// FrameworkDockerfile uses buildctl with the dockerfile frontend.
	FrameworkDockerfile BuildFramework = "dockerfile"
	// FrameworkAuto lets railpack auto-detect (Node vs Python).
	FrameworkAuto BuildFramework = "auto"
)

// GoBuildToolchain is the patched Go release Railpack installs for go124 and
// go124-alpine function builds and the Go starters' go.mod names (issue
// #2641). The runtime names are historical. Railpack applies this pin over
// the source's go.mod, so it is an exact release: Railpack 0.38 resolved the
// line "1.26" to 1.26.9, one patch behind. The go.mod go line still sets the
// module's language version and GODEBUG defaults. Keep it equal to go.mod's
// toolchain directive (TestGoToolchainVersionMatchesRepositoryToolchain).
const GoBuildToolchain = "1.26.10"

var goCommandNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidGoCommand reports whether name can select a `cmd/<name>` Go build
// (ADR-20261025134512): what Railpack's RAILPACK_GO_BIN and a shell word
// accept without quoting surprises.
func ValidGoCommand(name string) bool { return goCommandNameRE.MatchString(name) }

// BuildManifest is the /etc/faas/build.json contract — the single handoff
// from builderd (host) to guest-init (inside the builder VM). Its fields
// cover everything guest-init needs to run one build to completion without
// further host contact.
// BuildLayerCompressionZstd is the only BuildManifest.LayerCompression value.
const BuildLayerCompressionZstd = "zstd"

type BuildManifest struct {
	BuildDependencyDigest string            `json:"build_dependency_digest,omitempty"`
	PublicBuildURLs       map[string]string `json:"public_build_urls,omitempty"`
	SchemaVersion         int               `json:"schema_version"`
	BuildID               string            `json:"build_id"`
	TenantID              string            `json:"tenant_id"`
	DeploymentID          string            `json:"deployment_id"`
	SourceTarPath         string            `json:"source_tar_path"`           // absolute path on drive1
	BuildContext          string            `json:"build_context,omitempty"`   // extracted repository context
	Workdir               string            `json:"workdir"`                   // default /build/src
	DockerfilePath        string            `json:"dockerfile_path,omitempty"` // relative to Workdir; empty = Dockerfile
	// DockerfileBuild is the Compose build arguments and target stage
	// (ADR-20261019142544). Only Dockerfile builds receive it, and the guest
	// acknowledges its digest so an older guest cannot silently ignore it.
	DockerfileBuild *DockerfileBuildOptions `json:"dockerfile_build,omitempty"`
	// GoCommand builds `cmd/<GoCommand>` of the Go module at Workdir
	// (Railpack's RAILPACK_GO_BIN) for a Railpack build (ADR-20261025134512).
	// The guest echoes it in BuildDone.GoCommand, so an older guest, which
	// would build Railpack's default command, cannot pass for a newer one.
	GoCommand string         `json:"go_command,omitempty"`
	OutDir    string         `json:"out_dir"` // default /build/out
	Framework BuildFramework `json:"framework"`
	// Runtime and RuntimeBaseRef make the builder output reproducible with
	// imaged's deployment layer. Railpack otherwise chooses its own mutable
	// runtime base (currently railpack-runtime), which cannot be consumed by
	// the pinned Gregale runner base used during image materialisation.
	Runtime        string `json:"runtime,omitempty"`
	RuntimeBaseRef string `json:"runtime_base_ref,omitempty"`
	// Source-map publication uses a second, VM-local exporter of the SAME
	// BuildKit result. Older guests omit the sidecar and fail the host's opt-in
	// readiness check rather than silently publishing an unsymbolicated build.
	IssueSourceMaps *IssueSourceMapBuildConfig `json:"issue_source_maps,omitempty"`
	SourceSHA256    string                     `json:"source_sha256,omitempty"`
	// Function identifies source builds whose public contract is a handler
	// rather than a long-running process. The guest uses this bit to provide a
	// deterministic copy-only plan for handler-only Node/Python sources that
	// intentionally have no package manifest for Railpack to detect.
	Function bool `json:"function,omitempty"`
	// SharedRuntime marks a container app's source build on a shared runner
	// base (docs/adr/20261017-shared-runtime-source-apps.md): Runtime and
	// RuntimeBaseRef name that base, and the guest leaves system packages to
	// it instead of running Railpack's default apt step.
	SharedRuntime bool `json:"shared_runtime,omitempty"`
	// DependencyCache enables the app-scoped BuildKit cache exporter. Import is
	// set only when builderd successfully staged a prior cache into this
	// otherwise-ephemeral builder VM.
	DependencyCache       bool `json:"dependency_cache,omitempty"`
	DependencyCacheImport bool `json:"dependency_cache_import,omitempty"`
	// LayerCompression is "" (BuildKit's gzip) or BuildLayerCompressionZstd,
	// which compresses the exported OCI image and the dependency cache with
	// zstd (builderd's FAAS_BUILD_LAYER_ZSTD, ADR-20261028084018). An older
	// guest ignores it and keeps gzip.
	LayerCompression string `json:"layer_compression,omitempty"`
	// KeepWarm leaves a successful builder guest alive in a host-controlled
	// wait state so vmmd can capture a paused snapshot. Failed builds still
	// power off immediately; the warm path is only a successful-build handoff.
	KeepWarm bool `json:"keep_warm,omitempty"`
	// DevWatchCommand, when set, builds a watch-mode developer image
	// (ADR-1170): no build step, source shipped unchanged, this command as the
	// start command. Only builderd sets it, and only for developer apps.
	DevWatchCommand string `json:"dev_watch_command,omitempty"`
	// DevWatchHost is the developer app's public hostname, set with
	// DevWatchCommand; the image exposes it as DevWatchHostEnv.
	DevWatchHost string `json:"dev_watch_host,omitempty"`
	TimeoutSec   int    `json:"timeout_sec"`
	LogTailBytes int    `json:"log_tail_bytes"` // default 64 KiB
	// DockerHubMirrors are the operator's Docker Hub mirrors, which the
	// builder's BuildKit daemon tries before Docker Hub
	// (ADR-20261025114554). Empty pulls from Docker Hub directly.
	DockerHubMirrors []string `json:"docker_hub_mirrors,omitempty"`
}

// BuildDone is the /etc/faas/build-done.json contract — what guest-init
// writes after the build exits (success or failure). builderd reads this
// off the drive1 export to classify the result and pick the produced OCI
// tarball's host path.
//
// Error-explanations cluster (spec §6.4 amendment 1): FailureCode carries
// the RFC 7807 stable code for the failure (app_arch_mismatch /
// dep_install_failed) when guest-init could identify the root cause at
// exit time. Empty when guest-init fell back to a coarse
// FailureClass only (builds pre-cluster, or builds that died before
// classification ran). builderd's classifyBuildFailure surfaces the
// FailureCode through to pkg/state.SetDeploymentFailedEx so the
// customer sees hint/why/fix from pkg/whycopy rather than the legacy
// bare CodeDeployFailed.
type BuildDone struct {
	BuildDependencyDigest string `json:"build_dependency_digest,omitempty"`
	PublicBuildURLsSHA256 string `json:"public_build_urls_sha256,omitempty"`
	// DockerfileBuildSHA256 acknowledges the applied build arguments and
	// target (ADR-20261019142544); empty when the manifest carried none.
	DockerfileBuildSHA256 string `json:"dockerfile_build_sha256,omitempty"`
	// GoCommand acknowledges the Go command the build compiled
	// (ADR-20261025134512); empty when the manifest named none.
	GoCommand     string `json:"go_command,omitempty"`
	SchemaVersion int    `json:"schema_version"`
	BuildID       string `json:"build_id"`
	ExitCode      int    `json:"exit_code"`
	OCIImagePath  string `json:"oci_image_path"` // path on drive1, typically /build/out/image.tar
	LogTail       string `json:"log_tail"`
	FailureClass  string `json:"failure_class,omitempty"`
	// FailureCode is the RFC 7807 code guest-init identified (e.g.
	// "app_arch_mismatch" when the build VM's kernel returned
	// ENOEXEC on the customer binary; "dep_install_failed" with a
	// `pkg` discriminator via FailurePkg when the install step
	// itself exited non-zero). Mirrors pkg/api.Code… constants.
	FailureCode string `json:"failure_code,omitempty"`
	// FailurePkg is the package manager discriminator for
	// dep_install_failed (npm / pip / go / cargo / bundler).
	// Empty for every other code.
	FailurePkg string `json:"failure_pkg,omitempty"`
	// These are the exact tool versions guest-init observed in the builder
	// image. They remain empty when the VM dies before the probes can run.
	BuildkitVersion string `json:"buildkit_version,omitempty"`
	RailpackVersion string `json:"railpack_version,omitempty"`
	// DevPatch records, for a successful build, whether its source reached
	// the image unchanged (ADR-740). Older guests omit it.
	DevPatch *DevPatchSourceMap `json:"dev_patch,omitempty"`
	// DependencyCacheUnchanged reports that the build kept the imported
	// dependency cache instead of exporting it: its plan and install inputs
	// matched the ones that produced it. builderd keeps the stored cache.
	DependencyCacheUnchanged bool `json:"dependency_cache_unchanged,omitempty"`
	// SharedRuntime records, for a successful Node source build of an app,
	// whether its runtime artifact could run on the shared runner base
	// (docs/adr/1500-shared-runtime-source-apps.md). It is informational
	// until imaged consumes it; older guests omit it.
	SharedRuntime *SharedRuntimeVerdict `json:"shared_runtime,omitempty"`
}

// SharedRuntimeVerdict is the builder VM's classification of one Railpack
// plan. An eligible build needs only its /app tree on top of the runner base:
// Start is the resolved process argv and Env the plan's variables with
// toolchain paths removed. An ineligible build names one Reason.
type SharedRuntimeVerdict struct {
	Eligible bool              `json:"eligible"`
	Reason   string            `json:"reason,omitempty"`
	Start    []string          `json:"start,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// ValidBuildDependencyDigest accepts the optional internal cache partition.
func ValidBuildDependencyDigest(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
