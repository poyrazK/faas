package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// UploadSourceFormatOCIImageArchive marks a resumable upload as a prebuilt
// single-image OCI archive rather than a source tarball (ADR-20261127120000).
const UploadSourceFormatOCIImageArchive = "oci_image_archive"

// IsImageArchive reports whether the upload carries a prebuilt image.
func (o UploadDeployOptions) IsImageArchive() bool {
	return o.SourceFormat == UploadSourceFormatOCIImageArchive
}

// ValidateSourceFormat refuses an unknown format, and source-build options
// that mean nothing for an image that is already built.
func (o UploadDeployOptions) ValidateSourceFormat() *Problem {
	switch o.SourceFormat {
	case "":
		if o.ImageName != "" {
			return invalidUploadOptions("image_name requires source_format " + UploadSourceFormatOCIImageArchive)
		}
		if o.ReuseImageBase {
			return invalidUploadOptions("reuse_image_base requires source_format " + UploadSourceFormatOCIImageArchive)
		}
		return nil
	case UploadSourceFormatOCIImageArchive:
	default:
		return invalidUploadOptions("source_format must be empty or " + UploadSourceFormatOCIImageArchive)
	}
	if len(o.ImageName) > UploadImageNameMaxLen {
		return invalidUploadOptions(fmt.Sprintf("image_name is longer than %d characters", UploadImageNameMaxLen))
	}
	set := map[string]bool{
		"runtime": o.Runtime != "", "handler": o.Handler != "", "dockerfile": o.Dockerfile,
		"source_root": o.SourceRoot != "" && o.SourceRoot != ".",
		"source_url":  o.SourceURL != "", "commit_sha": o.CommitSHA != "",
	}
	var conflicts []string
	for name, on := range set {
		if on {
			conflicts = append(conflicts, name)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return invalidUploadOptions("an image archive is already built; remove " + strings.Join(conflicts, ", "))
	}
	return nil
}

func invalidUploadOptions(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeValidation, "Invalid deploy_options", detail)
}

// ImageBase is GET /v1/apps/{app}/image-base: the app's latest uploaded
// image, whose layers a redeploy may leave out of its upload
// (ADR-20261127120000 amendment 1).
type ImageBase struct {
	ManifestDigest string           `json:"manifest_digest"`
	Layers         []ImageBaseLayer `json:"layers"`
}

// ImageBaseLayer is one layer blob of an image base.
type ImageBaseLayer struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// CodeImageBaseChanged refuses a thin image upload whose left-out layers
// the app's image base no longer holds.
const CodeImageBaseChanged = "image_base_changed"

// ErrImageBaseChanged refuses a thin upload the current base cannot fill:
// another deploy replaced it, or it was never stored. Upload the full image.
func ErrImageBaseChanged() *Problem {
	return NewProblem(http.StatusConflict, CodeImageBaseChanged, "Image base changed",
		"The upload left out layers the app's previous image held, and the app no longer has them (another deploy may have replaced it). Upload the full image.").
		WithDocs(docsBase + "/container-compatibility")
}

// ErrNoImageBase answers GET image-base for an app with no stored base.
func ErrNoImageBase(slug string) *Problem {
	return NewProblem(http.StatusNotFound, CodeNotFound, "No image base",
		fmt.Sprintf("app %q has no uploaded image to reuse layers from", slug))
}

// ErrImageArchiveUnavailable refuses an image archive upload while the
// local_image_deploys platform flag is off for the account.
func ErrImageArchiveUnavailable() *Problem {
	return NewProblem(http.StatusBadRequest, CodeValidation, "Local image deploys unavailable",
		"This account cannot upload image archives yet. Push the image to a registry and deploy it with --image REF@sha256:<digest>.").
		WithDocs(docsBase + "/container-compatibility")
}

// ErrImageArchiveTooLarge refuses an image archive over ImageArchiveMaxMB.
func ErrImageArchiveTooLarge(l Limits, observedBytes int64) *Problem {
	return NewProblem(http.StatusRequestEntityTooLarge, CodeSourceTooLarge,
		"Image too large",
		fmt.Sprintf("%s plan caps an uploaded image at %d MB. Use a smaller base or a multi-stage build, or push the image to a registry and deploy it with --image REF@sha256:<digest>.", l.Plan, l.ImageArchiveMaxMB())).
		WithLimit(l.ImageArchiveMaxBytes(), observedBytes).
		WithDocs(docsBase + "/container-compatibility")
}

// ErrImageArchiveInvalid refuses an upload that is not a usable image archive.
func ErrImageArchiveInvalid(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSourceInvalid, "Invalid image archive", detail).
		WithDocs(docsBase + "/container-compatibility")
}

// ErrImageArchiveSignedImageRequired refuses a local image for an app whose
// policy requires a signed image: a local archive carries no signature, and
// the platform's own build signature must not vouch for it.
func ErrImageArchiveSignedImageRequired(slug string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeValidation, "Signed image required",
		fmt.Sprintf("app %q requires signed images, and an uploaded local image carries no signature. Push the signed image to a registry and deploy it with --image REF@sha256:<digest>.", slug)).
		WithDocs(docsBase + "/container-compatibility")
}
