package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// When the registry cannot serve --image, `gregale doctor` reads the image
// from the local Docker store instead, so an image built on this machine (and
// deployed with `deploy --local-image`) gets the same metadata checks.

// doctorLocalImage is an image's config as the local Docker store holds it.
type doctorLocalImage struct {
	ID     string
	Config oci.ImageConfig
	// SizeBytes is the unpacked size Docker reports.
	SizeBytes int64
}

// doctorLocalImageReader looks an image up in the local Docker store; ok is
// false when there is no Docker client or no such image.
type doctorLocalImageReader func(ctx context.Context, ref string) (doctorLocalImage, bool)

// readLocalDockerImage runs `docker image inspect`, asking for the
// linux/amd64 variant first: a multi-platform store (containerd) otherwise
// answers with the host's platform. Older clients refuse --platform, so a
// failure retries without it.
func readLocalDockerImage(ctx context.Context, ref string) (doctorLocalImage, bool) {
	docker, err := newDoctorDocker()
	if err != nil {
		return doctorLocalImage{}, false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	out, err := docker.run(ctx, "image", "inspect", "--platform", "linux/amd64", "--format", "{{json .}}", ref)
	if err != nil {
		out, err = docker.run(ctx, "image", "inspect", "--format", "{{json .}}", ref)
	}
	if err != nil {
		return doctorLocalImage{}, false
	}
	img, err := parseDockerImageInspect([]byte(out))
	if err != nil {
		return doctorLocalImage{}, false
	}
	return img, true
}

// parseDockerImageInspect maps one `docker image inspect` object onto the
// image config shape, so the registry path's decoder reads it.
func parseDockerImageInspect(b []byte) (doctorLocalImage, error) {
	var inspect struct {
		ID           string          `json:"Id"`
		OS           string          `json:"Os"`
		Architecture string          `json:"Architecture"`
		Variant      string          `json:"Variant"`
		Size         int64           `json:"Size"`
		Config       json.RawMessage `json:"Config"`
	}
	if err := json.Unmarshal(b, &inspect); err != nil {
		return doctorLocalImage{}, fmt.Errorf("parse docker image inspect: %w", err)
	}
	if inspect.ID == "" {
		return doctorLocalImage{}, errors.New("docker image inspect returned no image")
	}
	doc := map[string]any{"os": inspect.OS, "architecture": inspect.Architecture, "variant": inspect.Variant}
	if len(inspect.Config) > 0 && string(inspect.Config) != "null" {
		doc["config"] = inspect.Config
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return doctorLocalImage{}, err
	}
	cfg, err := oci.ParseImageConfig(encoded)
	if err != nil {
		return doctorLocalImage{}, err
	}
	return doctorLocalImage{ID: inspect.ID, Config: cfg, SizeBytes: inspect.Size}, nil
}

// doctorLocalImageFallback reports whether a registry failure leaves room for
// a local image. A registry that served the image but with the wrong platform
// or a malformed manifest has the image, so its finding stands.
func doctorLocalImageFallback(err error) bool {
	var selection *oci.PlatformSelectionError
	return !errors.As(err, &selection) && !errors.Is(err, oci.ErrImageManifestInvalid)
}

// doctorLocalRegistryCheck turns the registry failure into a warning that
// says where the metadata came from and how such an image deploys.
func doctorLocalRegistryCheck(registryErr doctorCheck, ref string) doctorCheck {
	c := registryErr
	c.Status = "warn"
	c.Hint = "The registry could not serve this image, but your local Docker has it; the checks below read the local image. `deploy --image` pulls from the registry, so it would fail."
	c.Fix = "Deploy the local image with `gregale deploy --local-image " + ref + "`, or push it and deploy with --image."
	return c
}

// doctorLocalImageSize compares Docker's unpacked size with the plan's
// filesystem capacity; local images have no compressed layer sizes.
func doctorLocalImageSize(ctx context.Context, size int64) doctorCheck {
	sizes := formatDoctorMB(size) + " unpacked"
	limits, ok := doctorImagePlanLimits(ctx)
	if !ok || limits.EphemeralDiskMaxBytes() <= 0 {
		return doctorCheck{Name: "image-size", Status: "ok", Hint: sizes + ". Sign in to compare with your plan's filesystem capacity."}
	}
	capText := fmt.Sprintf("%s plan filesystem capacity %d MB", limits.Plan, limits.EphemeralDiskMaxMB())
	if size > limits.EphemeralDiskMaxBytes() {
		return doctorCheck{Name: "image-size", Status: "error", Code: api.CodeAppLayerTooBig, Hint: sizes + ": over the " + capText + ".", Fix: "Use a slimmer base or variant, remove build tools and caches in the same layer that installs them, or a larger plan."}
	}
	return doctorCheck{Name: "image-size", Status: "ok", Hint: sizes + "; within the " + capText + "."}
}

func describeDoctorLocalImage(ref string, img doctorLocalImage) *doctorImage {
	m := describeDoctorImage(oci.ImageInspection{Reference: ref, Digest: img.ID, Config: img.Config})
	m.Source = "local-docker"
	return m
}

// doctorLocalImageSourceNote is printed under the image header.
func doctorLocalImageSourceNote(img *doctorImage) string {
	if img.Source != "local-docker" {
		return ""
	}
	return "  source: local Docker store (the registry could not serve it); image ID " + img.Digest
}
