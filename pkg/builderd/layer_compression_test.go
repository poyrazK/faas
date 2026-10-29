package builderd_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
)

func TestParseLayerZstd(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "", want: ""},
		{raw: "  ", want: ""},
		{raw: "false", want: ""},
		{raw: "0", want: ""},
		{raw: "true", want: api.BuildLayerCompressionZstd},
		{raw: "1", want: api.BuildLayerCompressionZstd},
		{raw: " TRUE ", want: api.BuildLayerCompressionZstd},
		{raw: "zstd", wantErr: true},
		{raw: "yes", wantErr: true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := builderd.ParseLayerZstd(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseLayerZstd(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ParseLayerZstd(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
