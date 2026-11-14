package builderd_test

// adr: 20261029171525

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/builderd"
)

func TestParseDeferCacheExport(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    bool
		wantErr bool
	}{
		{raw: "", want: false},
		{raw: "false", want: false},
		// Refused until the cache export stops editing the drive on the
		// host (bug hunt H104).
		{raw: "true", wantErr: true},
		{raw: " 1 ", wantErr: true},
		{raw: "later", wantErr: true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := builderd.ParseDeferCacheExport(tc.raw)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("ParseDeferCacheExport(%q) = %v, %v; want %v, wantErr %v", tc.raw, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
