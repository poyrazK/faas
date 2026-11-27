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
		// vmmd stages the cache export run's inputs, so the switch turns on
		// again (ADR-20261029171525 amendment 2).
		{raw: "true", want: true},
		{raw: " 1 ", want: true},
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
