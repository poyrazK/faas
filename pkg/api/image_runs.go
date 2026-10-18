package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Image runs (ADR-1161): `docker run`-shaped one-task Job runs against a
// platform-managed runner job per account and image.

const (
	// ImageRunStatusPulling means the image is not materialized yet; call
	// again once GET /v1/jobs/{job} reports image_materialization_status
	// "ready".
	ImageRunStatusPulling = "pulling"
	// ImageRunStatusQueued means a one-task run was created.
	ImageRunStatusQueued = "queued"

	ImageRunPullMissing = "missing"
	ImageRunPullAlways  = "always"

	// ImageRunImageMaxBytes bounds the image reference, matching vmmd's
	// job image reference cap.
	ImageRunImageMaxBytes = 2048
	// ImageRunArgvMax matches the Job command cap.
	ImageRunArgvMax = 64

	// CodeImageRunCacheFull: every cached image (api.ImageRunCachedImages)
	// has queued or running work, so none can be evicted for a new image. 409.
	CodeImageRunCacheFull = "image_run_cache_full"
	// CodeImageRunCommandRequired: the image has no ENTRYPOINT or CMD and the
	// request supplied neither. 400.
	CodeImageRunCommandRequired = "image_run_command_required"
	// CodeImageRunManaged: PATCH or a name collision against a
	// platform-managed image-run runner job. 409.
	CodeImageRunManaged = "image_run_managed"
)

// CreateImageRunRequest is the body of POST /v1/image-runs.
type CreateImageRunRequest struct {
	// Image is the OCI reference to run.
	Image string `json:"image"`
	// Entrypoint replaces the image ENTRYPOINT when set.
	Entrypoint *[]string `json:"entrypoint,omitempty"`
	// Args replaces the image CMD when set. Without Args, an overridden
	// Entrypoint runs with no arguments, as `docker run --entrypoint` does.
	Args *[]string `json:"args,omitempty"`
	// Env is the per-run environment, validated like Job run env_overrides.
	Env map[string]string `json:"env,omitempty"`
	// RAMMB 0 means the Job default; TimeoutSec 0 means the plan's Job task
	// timeout ceiling. Both are capped by the plan's Job ceilings.
	RAMMB      int `json:"ram_mb,omitempty"`
	TimeoutSec int `json:"timeout_sec,omitempty"`
	// Pull is "missing" (default) or "always".
	Pull string `json:"pull,omitempty"`
	// Interactive holds the command until one client attaches to the run's
	// task (ADR-1330); TTY gives it a pseudo-terminal. TTY requires
	// Interactive.
	Interactive bool `json:"interactive,omitempty"`
	TTY         bool `json:"tty,omitempty"`
	// User and Workdir override the image's USER and WORKDIR, as
	// `docker run -u/-w` do (ADR-1790). User is `name|uid[:group|gid]`;
	// Workdir is an absolute path, created when missing.
	User    string `json:"user,omitempty"`
	Workdir string `json:"workdir,omitempty"`
	// RegistryAuth authenticates the pull of a private image (ADR-1790). It
	// is stored sealed on the runner job for the image's registry, as
	// PUT /v1/jobs/{name}/registry-credentials would, and never returned.
	RegistryAuth *ImageRunRegistryAuth `json:"registry_auth,omitempty"`
}

// ImageRunRegistryAuth is a registry username and password or token for one
// image run's registry.
type ImageRunRegistryAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

const (
	// ImageRunUserMaxBytes bounds an image run's user override.
	ImageRunUserMaxBytes = 256
	// ImageRunWorkdirMaxBytes bounds an image run's working directory.
	ImageRunWorkdirMaxBytes = 4096
)

// ValidateImageRunWorkdir accepts an absolute path, as `docker run -w`
// requires. Empty keeps the image WORKDIR.
func ValidateImageRunWorkdir(dir string) error {
	if dir == "" {
		return nil
	}
	if len(dir) > ImageRunWorkdirMaxBytes || !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, "\x00\r\n") {
		return errors.New("workdir must be an absolute path of at most 4096 bytes")
	}
	return nil
}

const (
	// ImageRunAttachTimeoutSec is how long an interactive run's VM waits for
	// its client to attach (ADR-1330).
	ImageRunAttachTimeoutSec = 60
	// ImageRunInteractiveMaxTimeoutSec is the session limit of an
	// interactive run.
	ImageRunInteractiveMaxTimeoutSec = 3600
)

// ImageRunResponse answers POST /v1/image-runs. Job names the runner job;
// once Status is "queued", RunID addresses the run under /v1/jobs/{job}/runs.
type ImageRunResponse struct {
	Status      string   `json:"status"`
	Job         string   `json:"job"`
	Image       string   `json:"image"`
	ImageStatus string   `json:"image_status"`
	ImageError  string   `json:"image_error,omitempty"`
	ImageDigest string   `json:"image_digest,omitempty"`
	RunID       string   `json:"run_id,omitempty"`
	Command     []string `json:"command,omitempty"`
	// Attach is the one-time credential of an interactive run (ADR-1330),
	// returned only in the response that created the run.
	Attach *AppTaskAttachInfo `json:"attach,omitempty"`
}

// ResolveImageRunCommand applies Docker's rules: the argv is the effective
// entrypoint followed by the effective args. An overridden entrypoint drops
// the image CMD unless args are given.
func ResolveImageRunCommand(imageEntrypoint, imageCmd []string, entrypoint, args *[]string) []string {
	ep, rest := imageEntrypoint, imageCmd
	if entrypoint != nil {
		ep, rest = *entrypoint, nil
	}
	if args != nil {
		rest = *args
	}
	return append(append([]string{}, ep...), rest...)
}

// ErrImageRunCacheFull reports that no cached image could be evicted.
func ErrImageRunCacheFull(plan Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusConflict, CodeImageRunCacheFull,
		"Image run cache full",
		fmt.Sprintf("the %s plan caches %d images for image runs and all %d have queued or running work; wait for a run to finish or delete a runner job.",
			plan, limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#jobs")
}

// ErrImageRunCommandRequired reports an image with nothing to run.
func ErrImageRunCommandRequired(image string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeImageRunCommandRequired,
		"Command required",
		fmt.Sprintf("image %s has no ENTRYPOINT or CMD; pass a command to run.", image))
}

// ErrImageRunManaged reports a write against a platform-managed runner job,
// or a runner name already held by a customer job.
func ErrImageRunManaged(name string) *Problem {
	return NewProblem(http.StatusConflict, CodeImageRunManaged,
		"Image run job is platform-managed",
		fmt.Sprintf("job %q backs image runs and is managed by the platform; use POST /v1/image-runs or delete it.", name))
}
