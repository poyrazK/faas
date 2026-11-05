package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type laggingUsageProvider struct {
	qualificationProvider
	missing int
	windows []UsageWindow
}

func (p *laggingUsageProvider) Usage(_ context.Context, _ string, window UsageWindow) (Usage, error) {
	p.windows = append(p.windows, window)
	if len(p.windows) <= p.missing {
		return Usage{}, ErrNotFound
	}
	return Usage{Window: window, Readings: []MeterReading{{Meter: MeterComputeUnitSeconds, Quantity: 1}}}, nil
}

// A fresh resource has no usage before the hour it was created in, so the
// check reads that hour, and with WaitForUsage retries until the provider
// reports it.
func TestQualificationUsageReadsTheCreationHour(t *testing.T) {
	created := time.Now().UTC().Add(-2 * time.Hour)
	want := UsageWindow{From: created.Truncate(time.Hour), To: created.Truncate(time.Hour).Add(time.Hour)}
	tests := []struct {
		name      string
		wait      bool
		missing   int
		wantErr   error
		wantReads int
	}{
		{name: "reads once without waiting", missing: 1, wantErr: ErrNotFound, wantReads: 1},
		{name: "retries until the provider catches up", wait: true, missing: 2, wantReads: 3},
		{name: "reports the provider's error on timeout", wait: true, missing: 1 << 30, wantErr: ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			p := &laggingUsageProvider{missing: tc.missing}
			usage, err := qualifyProviderUsage(ctx, p, "resource", created, time.Millisecond, tc.wait)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantReads > 0 && len(p.windows) != tc.wantReads {
				t.Fatalf("reads = %d, want %d", len(p.windows), tc.wantReads)
			}
			for _, window := range p.windows {
				if !window.From.Equal(want.From) || !window.To.Equal(want.To) {
					t.Fatalf("window = %+v, want the creation hour %+v", window, want)
				}
			}
			if tc.wantErr == nil && !usage.Window.From.Equal(want.From) {
				t.Fatalf("usage = %+v", usage)
			}
		})
	}
}
