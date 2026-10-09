package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppLabelMarksDeletedApps(t *testing.T) {
	slugs := map[string]string{"live-id": "web"}
	for _, tc := range []struct {
		slugs map[string]string
		id    string
		want  string
	}{
		{slugs, "live-id", "web"},
		{slugs, "gone-id", "(deleted) gone-id"},
		{nil, "gone-id", "gone-id"}, // the app list failed: no claim either way
	} {
		if got := appLabel(tc.slugs, tc.id); got != tc.want {
			t.Errorf("appLabel(%v, %q) = %q, want %q", tc.slugs, tc.id, got, tc.want)
		}
	}
}

func TestRenderAppMetricsNamesTheSlug(t *testing.T) {
	var out bytes.Buffer
	renderAppMetrics(&out, "web", api.AppMetricsResponse{AppID: "app-id", Range: "5m", Source: "prometheus"})
	if !strings.Contains(out.String(), "Slug:       web") {
		t.Fatalf("metrics header lacks the slug:\n%s", out.String())
	}
}
