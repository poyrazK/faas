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

	src := &api.ProfileSourceLocation{URL: "https://github.com/acme/app/blob/" + strings.Repeat("a", 40) + "/app.js#L4", Path: "app.js", Line: 4}
	view := api.ProfileCaptureView{Kind: "heap", Unit: "bytes", Total: 3 << 20, Functions: []api.ProfileCaptureFunction{{Name: "<script>grow", File: "app.js", Line: 4, Self: 2 << 20, Total: 3 << 20, Source: src}},
		Flamegraph: &api.ProfileCaptureStack{Name: "all", Value: 3 << 20, Children: []*api.ProfileCaptureStack{{Name: "<script>grow", File: "app.js", Line: 4, Source: src, Value: 3 << 20, Children: []*api.ProfileCaptureStack{}}}}}
	page := ProfileCapturePage{AppSlug: "profile-app", Capture: capture, Kinds: []ProfileCaptureKindView{BuildProfileCaptureKind("profile-app", capture.ID, view)}}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profile_capture", Data: page}); err != nil {
		t.Fatal(err)
	}
	body = rec.Body.String()
	if strings.Contains(body, "<script>grow") || !strings.Contains(body, "&lt;script&gt;grow") {
		t.Fatal("function symbol was not escaped")
	}
	if strings.Contains(body, `"<script>grow"`) {
		t.Fatal("flamegraph JSON was not escaped for the script context")
	}
	for _, want := range []string{"3.00 MiB", "2.00 MiB", "app.js:4", "/pprof?kind=heap", `value="66.7"`, `data-flamegraph="heap"`, `href="` + src.URL + `"`, `"unit":"bytes"`} {
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

func TestProfileCaptureStacksRender(t *testing.T) {
	now := time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC)
	capture := api.ProfileCapture{ID: "11111111-1111-1111-1111-111111111111", Status: api.ProfileCaptureReady, Kinds: []string{"stacks", "cpu"}, DurationSeconds: 5,
		Trigger: api.ProfileCaptureTriggerSlowRequest, TriggerDetail: "GET /orders/{id} in flight for 12s", RequestID: "req-abc", Processes: 1, CreatedAt: now}
	view := api.ProfileCaptureView{Kind: "stacks", Unit: "count", Total: 7, Functions: []api.ProfileCaptureFunction{{Name: "main.profileFixtureHang", Self: 3, Total: 3}},
		Flamegraph: &api.ProfileCaptureStack{Name: "all", Value: 7, Children: []*api.ProfileCaptureStack{{Name: "main.profileFixtureHang", Value: 3, Children: []*api.ProfileCaptureStack{}}}}}
	page := ProfileCapturePage{AppSlug: "profile-app", Capture: capture, Kinds: []ProfileCaptureKindView{BuildProfileCaptureKind("profile-app", capture.ID, view)}}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profile_capture", Data: page}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{"stacks: 7 threads", "3 threads", "while a request was stuck", "GET /orders/{id} in flight for 12s",
		`href="/dashboard/apps/profile-app/debug?request_id=req-abc#request-detail"`, `"unit":"count"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("stacks capture page missing %q", want)
		}
	}
	if got := FormatProfileAmount(1, "count"); got != "1 thread" {
		t.Fatalf("FormatProfileAmount(1, count) = %q", got)
	}
}

func TestProfileCaptureOOMAndCompareRender(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	killed := now.Add(8 * time.Second)
	capture := api.ProfileCapture{ID: "11111111-1111-1111-1111-111111111111", Status: api.ProfileCaptureReady, Kinds: []string{"heap"}, DurationSeconds: 10,
		Trigger: api.ProfileCaptureTriggerMemoryPressure, Partial: true, OOMKilledAt: &killed, OOMPeakMB: 263, OOMPlanMB: 256, CreatedAt: now}
	earlier := api.ProfileCapture{ID: "33333333-3333-3333-3333-333333333333", Status: api.ProfileCaptureReady, Kinds: []string{"cpu", "heap"}, DurationSeconds: 10, CreatedAt: now.Add(-time.Hour)}
	page := ProfileCapturePage{AppSlug: "profile-app", Capture: capture, Compare: ProfileCaptureCompareOptions([]api.ProfileCapture{earlier})}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profile_capture", Data: page}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{"OOM-killed at 2026-10-14 09:30:08Z", "peaking at 263 MB of its 256 MB plan",
		`action="/dashboard/apps/profile-app/profiles/captures/` + capture.ID + `/diff"`, `value="` + earlier.ID + `"`, "2026-10-14 08:30Z · cpu&#43;heap · 10s"} {
		if !strings.Contains(body, want) {
			t.Fatalf("capture page missing %q", want)
		}
	}

	panel := ProfileCapturesView{AppSlug: "profile-app", CSRF: "tok", MaxDuration: 60, Captures: []api.ProfileCapture{capture}}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profiles", Data: struct {
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
	}{AppSlug: "profile-app", Captures: &panel}}); err != nil {
		t.Fatal(err)
	}
	if body := rec.Body.String(); !strings.Contains(body, "(ended early)") || !strings.Contains(body, "OOM-killed after") {
		t.Fatal("capture panel lacks the partial and OOM markers")
	}
}

func TestDeploymentOOMLinksHeapCapture(t *testing.T) {
	data := DeploymentDetailData{App: AppListItem{Slug: "profile-app"}, OOMCaptureURL: ProfileCaptureURL("profile-app", "11111111-1111-1111-1111-111111111111")}
	data.Deployment.ErrorCode = api.CodeAppRuntimeOOM
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "deployment_detail", Data: data}); err != nil {
		t.Fatal(err)
	}
	if body := rec.Body.String(); !strings.Contains(body, `href="/dashboard/apps/profile-app/profiles/captures/11111111-1111-1111-1111-111111111111"`) ||
		!strings.Contains(body, "Heap profile before the OOM kill") {
		t.Fatal("OOM explanation lacks the heap capture link")
	}
	data.OOMCaptureURL = ""
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "deployment_detail", Data: data}); err != nil || strings.Contains(rec.Body.String(), "Heap profile before the OOM kill") {
		t.Fatalf("link rendered without a capture: %v", err)
	}
}

func TestProfileCaptureDiffRender(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	diff := api.ProfileCaptureDiff{
		Baseline:  api.ProfileCapture{ID: "33333333-3333-3333-3333-333333333333", DurationSeconds: 10, CreatedAt: now.Add(-time.Hour)},
		Candidate: api.ProfileCapture{ID: "11111111-1111-1111-1111-111111111111", DurationSeconds: 10, CreatedAt: now, Trigger: api.ProfileCaptureTriggerMemoryPressure},
		Kind:      "heap", Unit: "bytes", BaselineTotal: 1 << 20, CandidateTotal: 5 << 20, Delta: 4 << 20,
		Functions: []api.ProfileCaptureFunctionDelta{{Name: "<script>grow", File: "app.js", Line: 4, Baseline: 1 << 20, Candidate: 5 << 20, Delta: 4 << 20}, {Name: "trim", Baseline: 2048, Delta: -2048}},
		Flamegraph: &api.ProfileCaptureStackDelta{Name: "all", Baseline: 1 << 20, Candidate: 5 << 20, Delta: 4 << 20, Children: []*api.ProfileCaptureStackDelta{
			{Name: "<script>grow", Baseline: 1 << 20, Candidate: 5 << 20, Delta: 4 << 20, Children: []*api.ProfileCaptureStackDelta{}}}},
		Warnings: []string{"The captures come from different deployments; code changes can move cost between functions."},
	}
	page := BuildProfileCaptureDiff("profile-app", diff)
	if page.Delta != "+4.00 MiB" || len(page.Rows) != 2 || !page.Rows[0].Grew || page.Rows[1].Delta != "−2.0 KiB" || page.Rows[1].Grew {
		t.Fatalf("diff page = %+v", page)
	}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profile_capture_diff", Data: page}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>grow") || strings.Contains(body, `"<script>grow"`) {
		t.Fatal("function symbol was not escaped")
	}
	for _, want := range []string{"&#43;4.00 MiB", "1.00 MiB", "5.00 MiB", "different deployments", `id="diff-flamegraph"`, `"delta":4194304`,
		`href="/dashboard/apps/profile-app/profiles/captures/` + diff.Baseline.ID + `"`, "memory pressure"} {
		if !strings.Contains(body, want) {
			t.Fatalf("diff page missing %q", want)
		}
	}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "n", Page{Body: "app_profile_capture_diff", Data: ProfileCaptureDiffPage{AppSlug: "profile-app", Error: "base must name another capture of this app"}}); err != nil {
		t.Fatal(err)
	}
	if body := rec.Body.String(); !strings.Contains(body, "base must name another capture") || strings.Contains(body, `id="diff-flamegraph"`) {
		t.Fatal("error page renders a comparison")
	}
	if got := ProfileCaptureDiffURL("profile-app", "id", "base-id", "heap"); got != "/dashboard/apps/profile-app/profiles/captures/id/diff?base=base-id&kind=heap" {
		t.Fatalf("diff URL = %q", got)
	}
}
