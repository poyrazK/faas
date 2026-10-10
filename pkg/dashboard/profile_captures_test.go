package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProfileCapturePanelAndPageRender(t *testing.T) {
	now := time.Now()
	capture := api.ProfileCapture{ID: "11111111-1111-1111-1111-111111111111", Status: api.ProfileCaptureReady, Kinds: []string{"cpu", "heap"},
		DurationSeconds: 10, InstanceID: "22222222-2222-2222-2222-222222222222", Processes: 1, CreatedAt: now}
	data := struct {
		RouteView       *ProfileRouteView
		CanaryFinding   any
		AppSlug         string
		Deployments     []struct{ ID, CommitSHA string }
		Query, Baseline api.ProfileQuery
		Profile         *api.ProfileResponse
		Compare         *api.ProfileCompareResponse
		Error           string
		Investigations  *ProfileInvestigationsView
		Automatic       *ProfileDeploymentChecksView
		CPUChart        *ProfileCPUChart
		Captures        *ProfileCapturesView
	}{AppSlug: "profile-app", Captures: &ProfileCapturesView{AppSlug: "profile-app", CSRF: "tok", MaxDuration: 60, Captures: []api.ProfileCapture{capture}}}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profiles", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{`action="/dashboard/apps/profile-app/profiles/captures"`, `value="tok"`, `/profiles/captures/` + capture.ID} {
		if !strings.Contains(body, want) {
			t.Fatalf("capture panel missing %q", want)
		}
	}

	view := api.ProfileCaptureView{Kind: "heap", Unit: "bytes", Total: 3 << 20, Functions: []api.ProfileCaptureFunction{{Name: "<script>grow", File: "app.js", Line: 4, Self: 2 << 20, Total: 3 << 20}}}
	page := ProfileCapturePage{AppSlug: "profile-app", Capture: capture, Kinds: []ProfileCaptureKindView{BuildProfileCaptureKind("profile-app", capture.ID, view)}}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profile_capture", Data: page}); err != nil {
		t.Fatal(err)
	}
	body = rec.Body.String()
	if strings.Contains(body, "<script>grow") || !strings.Contains(body, "&lt;script&gt;grow") {
		t.Fatal("function symbol was not escaped")
	}
	for _, want := range []string{"3.00 MiB", "2.00 MiB", "app.js:4", "/pprof?kind=heap", `value="66.7"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("capture page missing %q", want)
		}
	}
	if strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatal("finished capture keeps refreshing")
	}
	page.Refresh = true
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profile_capture", Data: page}); err != nil || !strings.Contains(rec.Body.String(), `http-equiv="refresh"`) {
		t.Fatalf("running capture does not refresh: %v", err)
	}
}

func TestFormatProfileAmount(t *testing.T) {
	for _, tt := range []struct {
		v    int64
		unit string
		want string
	}{{512, "bytes", "512 B"}, {2048, "bytes", "2.0 KiB"}, {1500000, "nanoseconds", "1.5ms"}, {3 << 30, "bytes", "3.00 GiB"}} {
		if got := FormatProfileAmount(tt.v, tt.unit); got != tt.want {
			t.Fatalf("FormatProfileAmount(%d, %s) = %q, want %q", tt.v, tt.unit, got, tt.want)
		}
	}
}
