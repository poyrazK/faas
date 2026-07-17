package api

// BuildManifestPath is where builderd writes the manifest onto drive1 at
// CreateColdBoot time and where guest-init reads it at boot to decide
// "build mode" vs "app mode" (M6 / spec §4.5).
const BuildManifestPath = "/etc/faas/build.json"

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
	SourceTarPath string         `json:"source_tar_path"` // absolute path on drive1
	Workdir       string         `json:"workdir"`         // default /build/src
	OutDir        string         `json:"out_dir"`         // default /build/out
	Framework     BuildFramework `json:"framework"`
	TimeoutSec    int            `json:"timeout_sec"`
	LogTailBytes  int            `json:"log_tail_bytes"` // default 64 KiB
}

// BuildDone is the /etc/faas/build-done.json contract — what guest-init
// writes after the build exits (success or failure). builderd reads this
// off the drive1 export to classify the result and pick the produced OCI
// tarball's host path.
type BuildDone struct {
	SchemaVersion int    `json:"schema_version"`
	BuildID       string `json:"build_id"`
	ExitCode      int    `json:"exit_code"`
	OCIImagePath  string `json:"oci_image_path"` // path on drive1, typically /build/out/image.tar
	LogTail       string `json:"log_tail"`
	FailureClass  string `json:"failure_class,omitempty"`
}
