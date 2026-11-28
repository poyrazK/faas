package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/oci"
)

// `gregale deploy --local-image NAME --boot` runs the image with local Docker
// before saving and uploading it, the same checks as
// `gregale doctor --image NAME --boot`, and stops the deploy on a failing
// one: a crash, a wrong listener, an out-of-memory kill or a failing health
// check costs an upload and a deployment otherwise.

// deployBootFlagProblem refuses --boot and --boot-env-file without a local
// image. A registry image is checked with `doctor --image REF --boot`.
func deployBootFlagProblem(localImageName string, explicit map[string]bool) error {
	if !explicit["boot"] && !explicit["boot-env-file"] {
		return nil
	}
	if localImageName == "" {
		return errors.New("--boot requires --local-image; check a registry image with `gregale doctor --image REF --boot`")
	}
	if explicit["boot-env-file"] && !explicit["boot"] {
		return errors.New("--boot-env-file requires --boot")
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
}

// runDeployBootCheck boots the image and returns a nonzero exit code when a
// boot check failed; warnings are printed and do not stop the deploy.
func runDeployBootCheck(ctx context.Context, in deployBootFlags) int {
	if !jsonOutput {
		PrintProgress(osStderr, "booting %s with local Docker before deploying", in.image)
	}
	report := runDoctorImageChecksWithLocal(ctx, in.image, nil, localOnlyInspector{}, readLocalDockerImage)
	flags := &doctorImageFlags{image: in.image, boot: true, app: in.app, healthPath: in.healthPath, envFile: in.envFile}
	addDoctorBootChecks(ctx, &report, flags)
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
