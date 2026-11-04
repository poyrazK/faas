package devloop

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261021221906 — only a sync that becomes a live patch defers its build.
func TestBuildNotBefore(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	eligible := &api.DevPatchPreview{Eligible: true, ChangedPaths: 2}
	for _, tc := range []struct {
		name      string
		preview   *api.DevPatchPreview
		delivery  bool
		buildNow  bool
		window    time.Duration
		wantDefer bool
	}{
		{name: "live patch", preview: eligible, delivery: true, window: time.Minute, wantDefer: true},
		{name: "no preview", delivery: true, window: time.Minute},
		{name: "ineligible", preview: &api.DevPatchPreview{Reason: api.DevPatchReasonRebuildInput, ChangedPaths: 1}, delivery: true, window: time.Minute},
		{name: "no changes", preview: &api.DevPatchPreview{Eligible: true}, delivery: true, window: time.Minute},
		{name: "delivery off", preview: eligible, window: time.Minute},
		{name: "build now", preview: eligible, delivery: true, buildNow: true, window: time.Minute},
		{name: "coalescing off", preview: eligible, delivery: true},
	} {
		got := BuildNotBefore(now, tc.preview, tc.delivery, tc.buildNow, tc.window)
		if got.IsZero() == tc.wantDefer || (tc.wantDefer && !got.Equal(now.Add(tc.window))) {
			t.Fatalf("%s: BuildNotBefore = %v", tc.name, got)
		}
	}
}

func TestCoalesceWindow(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"": api.DevBuildCoalesceWindow, "20": 20 * time.Second, "0": 0,
		"-5": api.DevBuildCoalesceWindow, "soon": api.DevBuildCoalesceWindow,
	} {
		if got := CoalesceWindow(func(string) string { return raw }); got != want {
			t.Fatalf("CoalesceWindow(%q) = %v, want %v", raw, got, want)
		}
	}
}
