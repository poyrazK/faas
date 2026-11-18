package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
)

func doctorSizeInspection(t *testing.T, layerSizes ...int64) oci.ImageInspection {
	t.Helper()
	diffIDs := make([]string, len(layerSizes))
	layers := make([]imagechain.Descriptor, len(layerSizes))
	for i, size := range layerSizes {
		diffIDs[i] = imagechain.Digest([]byte(fmt.Sprintf("diff-%d", i)))
		layers[i] = imagechain.Descriptor{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: imagechain.Digest([]byte(fmt.Sprintf("layer-%d", i))), Size: size}
	}
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": diffIDs}})
	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        imagechain.Descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: imagechain.Digest(config), Size: int64(len(config))},
		"layers":        layers,
	})
	digest := imagechain.Digest(manifest)
	return oci.ImageInspection{SourceDigest: digest, Digest: digest, Evidence: &imagechain.Evidence{SourceManifest: manifest, Config: config}}
}

// Kroki feedback 2026-11-17: doctor --image skipped every size check, so a
// companion over the filesystem cap was found only by deploying it.
func TestDoctorImageSizeAgainstPlan(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok {
		t.Fatal("no Free plan limits")
	}
	capBytes := limits.EphemeralDiskMaxBytes()
	previous := doctorImagePlanLimits
	t.Cleanup(func() { doctorImagePlanLimits = previous })
	doctorImagePlanLimits = func(context.Context) (api.Limits, bool) { return limits, true }

	for _, tc := range []struct {
		name   string
		sizes  []int64
		status string
	}{
		{"small image fits", []int64{capBytes / 10, capBytes / 20}, "ok"},
		{"likely over once unpacked", []int64{capBytes / 3, capBytes / 4}, "warn"},
		{"compressed alone is over", []int64{capBytes/2 + 1, capBytes / 2}, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check, layers := doctorImageSize(context.Background(), doctorSizeInspection(t, tc.sizes...))
			if check.Status != tc.status {
				t.Fatalf("status = %s (%s), want %s", check.Status, check.Hint, tc.status)
			}
			if layers == nil || layers.Count != len(tc.sizes) || layers.Largest[0].CompressedBytes != tc.sizes[0] {
				t.Fatalf("layers = %+v", layers)
			}
			if tc.status != "ok" && (check.Code != api.CodeAppLayerTooBig || !strings.Contains(check.Fix, "companion")) {
				t.Fatalf("check = %+v, want the too-big code and a companion fix", check)
			}
		})
	}
}

func TestDoctorImageSizeWithoutPlanOrManifest(t *testing.T) {
	previous := doctorImagePlanLimits
	t.Cleanup(func() { doctorImagePlanLimits = previous })
	doctorImagePlanLimits = func(context.Context) (api.Limits, bool) { return api.Limits{}, false }

	check, layers := doctorImageSize(context.Background(), doctorSizeInspection(t, 5<<20))
	if check.Status != "ok" || layers == nil || !strings.Contains(check.Hint, "Sign in") {
		t.Fatalf("logged out check = %+v", check)
	}
	check, layers = doctorImageSize(context.Background(), oci.ImageInspection{})
	if check.Status != "skipped" || layers != nil {
		t.Fatalf("no manifest check = %+v", check)
	}
}
