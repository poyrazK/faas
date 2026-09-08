package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestRender_AppDetail_LogDrainPanel_MasksAuthAndShowsState(t *testing.T) {
	rec := httptest.NewRecorder()
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{Slug: "demo", AppID: "app-1", Status: "active"},
			LogDrains: []dashboard.LogDrainItem{
				{ID: "0123456789abcdef0123456789abcdef", Kind: "http_json", TargetURL: "https://logs.example/ingest", AuthHeaderMasked: "***", Enabled: true, UpdatedAt: "2026-09-08T09:00:00Z"},
				{ID: "fedcba9876543210fedcba9876543210", Kind: "otlp", TargetURL: "https://otel.example/v1/logs", Enabled: false},
			},
		},
	}
	if err := dashboard.Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "drain-panel-nonce", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{"Log drains", "http_json", "otlp", "https://logs.example/ingest", "enabled", "disabled", "***"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "Authorization:") {
		t.Fatal("dashboard rendered an auth header instead of its masked state")
	}
}

func TestRender_AppDetail_LogDrainPanel_EmptyState(t *testing.T) {
	rec := httptest.NewRecorder()
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data:  dashboard.AppDetailData{App: dashboard.AppListItem{Slug: "demo"}},
	}
	if err := dashboard.Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No log drains configured") || !strings.Contains(body, "gregale logs drain add --app demo") {
		t.Fatalf("empty-state hint missing: %s", body)
	}
}
