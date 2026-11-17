package imaged

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// workloadBuildError names the image a deployment's build failed on. A typed
// problem such as app_layer_too_big keeps its stable code and customer-safe
// detail in markDeployFailed, which used to drop the caller's
// `sidecar "<name>" build` prefix: with companions, the customer saw
// "requires 2112 MB" with no way to tell which image was too large.
type workloadBuildError struct {
	// Companion is the companion (sidecar) name; empty for the app image.
	Companion string
	Image     string
	err       error
}

func (e *workloadBuildError) Error() string {
	if e.Companion == "" {
		// Callers already prefix the app image's failures.
		return e.err.Error()
	}
	return fmt.Sprintf("sidecar %q build: %v", e.Companion, e.err)
}

func (e *workloadBuildError) Unwrap() error { return e.err }

// attributeCompanion wraps err with the companion and image it came from.
func attributeCompanion(err error, companion, image string) error {
	if err == nil || companion == "" {
		return err
	}
	return &workloadBuildError{Companion: companion, Image: image, err: err}
}

// attributeAppImage marks err as the app image's when the deployment also has
// companions; without companions there is only one image to blame.
func attributeAppImage(err error, dep state.Deployment) error {
	if err == nil || !hasCompanions(dep) {
		return err
	}
	return &workloadBuildError{Image: dep.ImageDigest, err: err}
}

func hasCompanions(dep state.Deployment) bool {
	raw := string(dep.Sidecars)
	return raw != "" && raw != "null" && raw != "[]"
}

// workloadOf reports the image an error is attributed to.
func workloadOf(err error) (*workloadBuildError, bool) {
	var w *workloadBuildError
	if errors.As(err, &w) {
		return w, true
	}
	return nil, false
}

// workloadDetail prefixes a customer-safe detail with the workload and its
// image reference.
func workloadDetail(w *workloadBuildError, detail string) string {
	name := "app image"
	if w.Companion != "" {
		name = fmt.Sprintf("companion %q", w.Companion)
	}
	if w.Image == "" {
		return name + ": " + detail
	}
	return fmt.Sprintf("%s (image %s): %s", name, w.Image, detail)
}
