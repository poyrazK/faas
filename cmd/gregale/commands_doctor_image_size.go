package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
)

// doctorImageLayerRatio is how much larger than its compressed layers an
// image's unpacked tree usually is. Compressed bytes are a hard lower bound;
// the ratio only decides when to warn.
const doctorImageLayerRatio = 2

// doctorImagePlanLimits returns the signed-in account's plan limits. It is a
// variable so tests run without an API. Best effort: doctor --image also runs
// logged out, against images not yet tied to an account.
var doctorImagePlanLimits = func(ctx context.Context) (api.Limits, bool) {
	client, err := authedClient()
	if err != nil {
		return api.Limits{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	acct, err := client.Whoami(ctx)
	if err != nil {
		return api.Limits{}, false
	}
	return api.LimitsFor(api.Plan(acct.Plan))
}

// doctorImageSize sums the selected image's compressed layers from the
// manifest the inspection already verified (no layer is downloaded) and
// compares them with the plan's filesystem capacity. Before, doctor --image
// skipped every size check, so an image over the cap was found only by a
// full deployment (Kroki feedback 2026-11-17).
func doctorImageSize(ctx context.Context, result oci.ImageInspection) (doctorCheck, *doctorImageLayers) {
	image, err := imagechain.Validate(result.Evidence, result.SourceDigest, result.Digest)
	if err != nil {
		return doctorCheck{Name: "image-size", Status: "skipped", Reason: "The registry did not return a verifiable image manifest, so layer sizes are unknown."}, nil
	}
	layers := summarizeDoctorImageLayers(image.Layers)
	sizes := fmt.Sprintf("%d layers, %s compressed", layers.Count, formatDoctorMB(layers.CompressedBytes))
	if len(layers.Largest) > 0 {
		parts := make([]string, 0, len(layers.Largest))
		for _, l := range layers.Largest {
			parts = append(parts, fmt.Sprintf("layer %d %s", l.Index, formatDoctorMB(l.CompressedBytes)))
		}
		sizes += "; largest: " + strings.Join(parts, ", ")
	}
	limits, ok := doctorImagePlanLimits(ctx)
	if !ok || limits.EphemeralDiskMaxBytes() <= 0 {
		return doctorCheck{Name: "image-size", Status: "ok", Hint: sizes + ". Sign in to compare with your plan's filesystem capacity; the unpacked image is usually 2-3x its compressed size."}, layers
	}
	capBytes := limits.EphemeralDiskMaxBytes()
	capText := fmt.Sprintf("%s plan filesystem capacity %d MB", limits.Plan, limits.EphemeralDiskMaxMB())
	fix := "Use a slimmer base or variant, remove build tools and caches in the same layer that installs them, or a larger plan. Check every companion image the same way: each one is built against this capacity on its own."
	switch {
	case layers.CompressedBytes > capBytes:
		return doctorCheck{Name: "image-size", Status: "error", Code: api.CodeAppLayerTooBig, Hint: sizes + ": over the " + capText + " even before unpacking.", Fix: fix}, layers
	case layers.CompressedBytes*doctorImageLayerRatio > capBytes:
		return doctorCheck{Name: "image-size", Status: "warn", Code: api.CodeAppLayerTooBig, Hint: sizes + ": unpacked, it is likely over the " + capText + " (filesystem overhead included). Deployment reports the exact size.", Fix: fix}, layers
	}
	return doctorCheck{Name: "image-size", Status: "ok", Hint: sizes + "; within the " + capText + " at the usual unpacked ratio."}, layers
}

// doctorImageLayers is the layer summary doctor reports.
type doctorImageLayers struct {
	Count           int                      `json:"count"`
	CompressedBytes int64                    `json:"compressed_bytes"`
	Largest         []doctorImageLayerDetail `json:"largest,omitempty"`
}

type doctorImageLayerDetail struct {
	Index           int    `json:"index"`
	Digest          string `json:"digest"`
	CompressedBytes int64  `json:"compressed_bytes"`
}

func summarizeDoctorImageLayers(descriptors []imagechain.Descriptor) *doctorImageLayers {
	out := &doctorImageLayers{Count: len(descriptors)}
	details := make([]doctorImageLayerDetail, 0, len(descriptors))
	for i, d := range descriptors {
		out.CompressedBytes += d.Size
		details = append(details, doctorImageLayerDetail{Index: i, Digest: d.Digest, CompressedBytes: d.Size})
	}
	sort.SliceStable(details, func(i, j int) bool { return details[i].CompressedBytes > details[j].CompressedBytes })
	if len(details) > 3 {
		details = details[:3]
	}
	out.Largest = details
	return out
}

func formatDoctorMB(b int64) string {
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
}
