package api

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

// BuildManifest is the /etc/faas/build.json contract — the single handoff
// from builderd (host) to guest-init (inside the builder VM). Its fields
// cover everything guest-init needs to run one build to completion without
// further host contact.
type BuildManifest struct {
	SchemaVersion int            `json:"schema_version"`
	BuildID       string         `json:"build_id"`
	TenantID      string         `json:"tenant_id"`
	DeploymentID  string         `json:"deployment_id"`
	SourceTarPath string         `json:"source_tar_path"`         // absolute path on drive1
	BuildContext  string         `json:"build_context,omitempty"` // extracted repository context
	Workdir       string         `json:"workdir"`                 // default /build/src
	OutDir        string         `json:"out_dir"`                 // default /build/out
	Framework     BuildFramework `json:"framework"`
	// Runtime and RuntimeBaseRef make the builder output reproducible with
	// imaged's deployment layer. Railpack otherwise chooses its own mutable
	// runtime base (currently railpack-runtime), which cannot be consumed by
	// the pinned Gregale runner base used during image materialisation.
	Runtime        string `json:"runtime,omitempty"`
	RuntimeBaseRef string `json:"runtime_base_ref,omitempty"`
	// DependencyCache enables the developer-session BuildKit cache exporter.
	// Import is set only when builderd successfully staged a prior cache into
	// this otherwise-ephemeral builder VM. Production builds leave both false.
	DependencyCache       bool `json:"dependency_cache,omitempty"`
	DependencyCacheImport bool `json:"dependency_cache_import,omitempty"`
	TimeoutSec            int  `json:"timeout_sec"`
	LogTailBytes          int  `json:"log_tail_bytes"` // default 64 KiB
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
}
