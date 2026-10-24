package dashboard_test

import (
	"html"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

// ADR-1155: the app page names retained captures the platform took on its
// own or that an OOM kill followed, newest first.
func TestAnnouncedProfileCapturesBanner(t *testing.T) {
	now := time.Date(2026, 10, 14, 21, 3, 0, 0, time.UTC)
	killed := now.Add(-time.Hour)
	heap := []api.ProfileCaptureProfile{{Kind: api.ProfileKindHeap, Bytes: 9}}
	captures := []api.ProfileCapture{
		{ID: "slow-1", Status: api.ProfileCaptureReady, Trigger: api.ProfileCaptureTriggerSlowRequest, TriggerDetail: "GET /orders in flight for 12s", Profiles: heap, CreatedAt: now},
		{ID: "manual-1", Status: api.ProfileCaptureReady, Trigger: api.ProfileCaptureTriggerManual, Profiles: heap, CreatedAt: now.Add(-time.Minute)},
		{ID: "empty-1", Status: api.ProfileCaptureReady, Trigger: api.ProfileCaptureTriggerSlowRequest, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "failed-1", Status: api.ProfileCaptureFailed, Trigger: api.ProfileCaptureTriggerMemoryPressure, CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "oom-1", Status: api.ProfileCaptureReady, Trigger: api.ProfileCaptureTriggerManual, Profiles: heap, OOMKilledAt: &killed, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "pressure-1", Status: api.ProfileCaptureReady, Trigger: api.ProfileCaptureTriggerMemoryPressure, Profiles: heap, OOMKilledAt: &killed, CreatedAt: now.Add(-3 * time.Hour)},
	}
	if got := dashboard.BuildAnnouncedProfileCaptures("demo", captures[1:4]); got != nil {
		t.Fatalf("manual, empty and failed captures announced: %+v", got)
	}
	view := dashboard.BuildAnnouncedProfileCaptures("demo", captures)
	if view == nil || view.Count != 3 || view.OOMKilled != 2 || view.Latest.ID != "slow-1" || view.Days != 7 ||
		view.LatestURL != "/dashboard/apps/demo/profiles/captures/slow-1" || view.LatestTrigger != "slow request" ||
		view.ListURL != "/dashboard/apps/demo/profiles#profile-captures" {
		t.Fatalf("banner = %+v", view)
	}

	rec := httptest.NewRecorder()
	page := dashboard.Page{Title: "demo", Body: "app_detail", Data: dashboard.AppDetailData{
		App: dashboard.AppListItem{Slug: "demo", AppID: "demo-uuid", Status: "active"}, AnnouncedCaptures: view}}
	if err := dashboard.Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := html.UnescapeString(rec.Body.String())
	for _, want := range []string{
		"Profile captures to review: 3</strong> in the last 7 days, 2 linked to an OOM kill.",
		`href="/dashboard/apps/demo/profiles/captures/slow-1">slow request · GET /orders in flight for 12s</a>`,
		"2026-10-14T21:03:00Z",
		`href="/dashboard/apps/demo/profiles#profile-captures">All captures</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("app page lacks %q", want)
		}
	}

	rec = httptest.NewRecorder()
	page.Data = dashboard.AppDetailData{App: dashboard.AppListItem{Slug: "demo", AppID: "demo-uuid", Status: "active"}}
	if err := dashboard.Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(rec.Body.String(), "Profile captures to review") {
		t.Error("banner rendered without announced captures")
	}
}
