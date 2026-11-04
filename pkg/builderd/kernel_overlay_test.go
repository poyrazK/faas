package builderd_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
)

func TestParseKernelOverlay(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "", want: ""},
		{raw: "false", want: ""},
		{raw: "true", want: api.BuildKitSnapshotterAuto},
		{raw: " 1 ", want: api.BuildKitSnapshotterAuto},
		{raw: "overlayfs", wantErr: true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := builderd.ParseKernelOverlay(tc.raw)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("ParseKernelOverlay(%q) = %q, %v; want %q, wantErr %v", tc.raw, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
