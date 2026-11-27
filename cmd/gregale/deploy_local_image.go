package main

// `gregale deploy --local-image NAME` and `--image-archive PATH` deploy an
// image that exists only on this machine (ADR-20261127120000). The CLI turns
// `docker save` output into a single-image linux/amd64 OCI archive and sends
// it through the resumable upload session. Everything before submission is
// the --image deploy path; only the submission differs.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociarchive"
)

// localImageDeploy is a converted image ready to upload.
type localImageDeploy struct {
	archivePath    string
	imageName      string // repository tag recorded on the deployment, if any
	displayName    string // what the --image path prints and checks
	manifestDigest string
}

// localImageIDPattern matches a short or full image ID, which names exactly
// one image and must not gain a ":latest" tag.
var localImageIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

// localImageConflicts are deploy flags a local image cannot honour: other
// sources, previews, and options the upload session does not carry.
var localImageConflicts = []string{
	"image", "tarball", "path", "repo", "repository", "template", "github", "project", "project-slug",
	"function", "runtime", "handler", "dockerfile", "worktree", "anonymous",
	"dry-run", "plan", "diff", "server-diff", "resume-upload-id",
	"traffic-percent", "no-traffic", "canary-preset", "canary-stages", "rollout", "safe",
	"readiness-path", "mcp-task-migration-override",
}

// localImageFlagProblem refuses an invalid local-image flag combination.
func localImageFlagProblem(name, archive string, explicit map[string]bool) error {
	if name == "" && archive == "" {
		return nil
	}
	if name != "" && archive != "" {
		return errors.New("--local-image and --image-archive are mutually exclusive")
	}
	flagName := "--local-image"
	if archive != "" {
		flagName = "--image-archive"
	}
	var conflicts []string
	for _, f := range localImageConflicts {
		if explicit[f] {
			conflicts = append(conflicts, "--"+f)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return fmt.Errorf("%s cannot be combined with %s", flagName, strings.Join(conflicts, ", "))
	}
	return nil
}

// prepareLocalImageDeploy validates the flags and converts the image. The
// cleanup removes every temporary file and is safe to call on failure.
func prepareLocalImageDeploy(ctx context.Context, name, archive string, explicit map[string]bool) (*localImageDeploy, func(), int) {
	noop := func() {}
	if !localImageIDPattern.MatchString(name) {
		name = ociarchive.WithDefaultTag(name) // save one image, not every tag of the repository
	}
	if err := localImageFlagProblem(name, archive, explicit); err != nil {
		return nil, noop, printErr("Invalid flags", err)
	}
	if name == "" && archive == "" {
		return nil, noop, 0
	}
	dir, err := os.MkdirTemp("", "gregale-local-image-")
	if err != nil {
		return nil, noop, printErr("Could not prepare the image", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	input := archive
	if name != "" {
		input = filepath.Join(dir, "docker-save.tar")
		if !jsonOutput {
			PrintProgress(osStderr, "saving %s from Docker", name)
		}
		if err := dockerSave(ctx, name, input); err != nil {
			cleanup()
			return nil, noop, printErr("Could not read the local image", err)
		}
	}
	prepared, err := convertLocalImage(input, name, dir)
	if name != "" {
		_ = os.Remove(input) // the canonical archive replaces it; halve the disk peak
	}
	if err != nil {
		cleanup()
		return nil, noop, printErr("Could not prepare the image", err)
	}
	if prepared.displayName == "" {
		prepared.displayName = strings.TrimSuffix(filepath.Base(archive), filepath.Ext(archive))
	}
	return prepared, cleanup, 0
}

func convertLocalImage(input, name, dir string) (*localImageDeploy, error) {
	out := filepath.Join(dir, "image.oci.tar")
	f, err := os.Create(out) //nolint:forbidigo // CLI-owned temporary file.
	if err != nil {
		return nil, err
	}
	res, convErr := ociarchive.Convert(input, name, dir, f)
	if closeErr := f.Close(); convErr == nil {
		convErr = closeErr
	}
	if convErr != nil {
		return nil, convErr
	}
	imageName := name
	if imageName == "" {
		imageName = res.ImageName
	}
	if len(imageName) > api.UploadImageNameMaxLen {
		imageName = imageName[:api.UploadImageNameMaxLen]
	}
	return &localImageDeploy{archivePath: out, imageName: imageName, displayName: imageName, manifestDigest: res.ManifestDigest}, nil
}

// localImageIntent is the image identity a deploy's idempotency intent
// records: the converted manifest digest, so rebuilding the same tag is a
// new deployment rather than a replay of the last one.
func localImageIntent(image string, img *localImageDeploy) string {
	if img == nil {
		return image
	}
	return "local-image@" + img.manifestDigest
}

// dockerSave writes `docker image save` output. GREGALE_DOCKER selects a
// compatible client such as podman.
func dockerSave(ctx context.Context, name, dest string) error {
	bin := strings.TrimSpace(os.Getenv("GREGALE_DOCKER"))
	if bin == "" {
		bin = "docker"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%s was not found; save the image yourself with `docker save -o image.tar %s` and deploy it with --image-archive image.tar", bin, name)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "image", "save", "-o", dest, name) //nolint:gosec // argv only, no shell; the binary is the customer's Docker client.
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("%s image save %s: %s", bin, name, detail)
	}
	return nil
}

// localImageUploadOptions maps the --image path's settings onto the upload
// session. Flags it cannot carry were refused by localImageFlagProblem.
func localImageUploadOptions(img *localImageDeploy, req api.CreateDeploymentRequest, healthcheck *api.DeploymentHealthcheck, noTriggers bool) api.UploadDeployOptions {
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	pr := 0
	if req.PRNumber != nil {
		pr = *req.PRNumber
	}
	return api.UploadDeployOptions{
		SourceFormat: api.UploadSourceFormatOCIImageArchive, ImageName: img.imageName,
		Healthcheck: healthcheck, Scope: req.Scope, Environment: req.Environment,
		Reason: deref(req.Reason), Tag: deref(req.Tag), DeployedBy: deref(req.DeployedBy), PRNumber: pr,
		Workflows: req.Workflows, Companions: req.Companions,
		RollbackOn5xx: req.RollbackOn5xx, DisableStartupCPUBoost: req.DisableStartupCPUBoost,
		NoTriggers: noTriggers, OverrideFreeze: req.OverrideFreeze,
		ReleaseAt: req.ReleaseAt, ReleaseNotify: req.ReleaseNotify,
	}
}

// submitLocalImage uploads the converted archive and commits it.
func submitLocalImage(ctx context.Context, client *Client, slug string, img *localImageDeploy, opts api.UploadDeployOptions, observe func(deployUploadIdentity) error) (api.DeploymentResponse, error) {
	var progress resumableUploadProgress
	if !jsonOutput {
		last := -1
		progress = func(uploaded, total int64) {
			percent := int(uploaded * 100 / total)
			if percent == last && uploaded != total {
				return
			}
			last = percent
			PrintProgress(osStderr, "uploading image %s: %d%% (%d/%d MiB)", img.displayName, percent, uploaded/(1024*1024), total/(1024*1024))
		}
	}
	dep, _, supported, err := deployResumableTarball(client, ctx, slug, img.archivePath, progress, observe, opts)
	if err == nil && !supported {
		err = errors.New("this Gregale API does not accept resumable uploads, which local image deploys need; push the image to a registry and deploy it with --image REF@sha256:<digest>")
	}
	return dep, err
}
