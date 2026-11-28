package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// `gregale deploy --local-image NAME --boot` runs the image with local Docker
// before saving and uploading it, the same checks as
// `gregale doctor --image NAME --boot`, and stops the deploy on a failing
// one: a crash, a wrong listener, an out-of-memory kill or a failing health
// check costs an upload and a deployment otherwise.

// deployBootFlagProblem refuses --boot without a local image to upload, and
// --boot-env-file without --boot. A registry image is checked with
// `doctor --image REF --boot`.
func deployBootFlagProblem(localImageName string, boot, createOnly bool, explicit map[string]bool) error {
	if explicit["boot-env-file"] && !boot {
		return errors.New("--boot-env-file requires --boot")
	}
	if !boot {
		return nil
	}
	if localImageName == "" {
		return errors.New("--boot requires --local-image; check a registry image with `gregale doctor --image REF --boot`")
	}
	if createOnly {
		return errors.New("--boot checks an image before it is uploaded, and --create-only uploads nothing")
	}
	return nil
}

// deployBootModeProblem refuses --boot for a deploy the boot check cannot
// judge: a gRPC-health service, which the local probe cannot ask, and an
// HTTP health path on a worker or job, which serves none (as doctor does).
// Workers and jobs are otherwise checked without HTTP (doctor_boot_mode.go).
func deployBootModeProblem(mode string, healthcheck *api.DeploymentHealthcheck) error {
	if healthcheck != nil && healthcheck.Path != "" && doctorBootNoListener(mode) {
		return fmt.Errorf("--healthcheck-path probes HTTP, which a %s does not serve", strings.TrimSpace(mode))
	}
	if healthcheck != nil && healthcheck.GRPC != nil {
		return errors.New("--boot probes HTTP, and this deploy checks readiness with gRPC health; deploy without --boot")
	}
	return nil
}

// localOnlyInspector sends doctor straight to the local Docker store: the
// image being deployed is local by definition.
type localOnlyInspector struct{}

func (localOnlyInspector) InspectImage(context.Context, string, *oci.BasicAuth) (oci.ImageInspection, error) {
	return oci.ImageInspection{}, errors.New("deploying a local image")
}

// deployBootFlags is the boot input a deploy supplies.
type deployBootFlags struct {
	image, app, healthPath, envFile string
	// mode is the deploy's resolved execution mode (flag or gregale.yaml).
	mode string
	// ramMB is the RAM of the deploy's --size, 0 to use the app's.
	ramMB int
}

// deployBootRAMMB is the RAM a --size profile gives the app, 0 when none.
func deployBootRAMMB(profile string) int {
	if spec, ok := api.ResourceProfileSpecFor(strings.TrimSpace(profile)); ok {
		return spec.MemoryMB
	}
	return 0
}

// runDeployBootCheck boots the image and returns a nonzero exit code when a
// boot check failed; warnings are printed and do not stop the deploy.
func runDeployBootCheck(ctx context.Context, in deployBootFlags) int {
	if !jsonOutput {
		PrintProgress(osStderr, "booting %s with local Docker before deploying", in.image)
	}
	report := runDoctorImageChecksWithLocal(ctx, in.image, nil, localOnlyInspector{}, readLocalDockerImage)
	flags := &doctorImageFlags{image: in.image, boot: true, app: in.app, healthPath: in.healthPath, envFile: in.envFile, ramMB: in.ramMB, deploy: true, executionMode: in.mode}
	if addDoctorBootChecks(ctx, &report, flags) {
		// Ctrl-C was meant to stop the deploy, not to skip the check.
		return printErr("Deploy interrupted", errors.New("the local boot check was interrupted; nothing was uploaded"))
	}
	boot := doctorReport{Image: report.Image, Checks: []doctorCheck{}}
	for _, c := range report.Checks {
		if strings.HasPrefix(c.Name, "boot-") {
			boot.Checks = append(boot.Checks, c)
		}
	}
	if !jsonOutput && (boot.HasErrors() || boot.HasWarnings()) {
		renderDoctorHumanExit(osStderr, boot, boot.HasErrors())
	}
	if !boot.HasErrors() {
		return 0
	}
	var failed []string
	for _, c := range boot.Checks {
		if c.Status == "error" {
			failed = append(failed, fmt.Sprintf("%s (%s)", c.Name, c.Code))
		}
	}
	return printErr("Local boot check failed", fmt.Errorf("%s; nothing was uploaded. Fix the image, or deploy without --boot", strings.Join(failed, ", ")))
}
