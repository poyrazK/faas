package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestRender_AppDetail_WorkflowPanel(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{Slug: "demo", AppID: "app-1", Status: "active"},
			Workflows: []dashboard.WorkflowRunItem{
				{
					ID:           "run-1",
					WorkflowName: "order_flow",
					Status:       "failed",
					CurrentStep:  "charge",
					CreatedAt:    "2026-09-05T10:00:00Z",
					StartedAt:    "2026-09-05T10:00:01Z",
					LastError:    "payment provider unavailable",
					Steps: []dashboard.WorkflowStepItem{
						{Name: "prepare", Status: "succeeded", Attempt: 1},
						{Name: "charge", Status: "dead", Attempt: 3, Error: "payment provider unavailable"},
					},
				},
			},
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Workflows",
		"order_flow",
		"failed",
		"run-1",
		"current step:",
		"prepare",
		"succeeded",
		"charge",
		"attempt 3",
		"payment provider unavailable",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestRender_AppDetail_WorkflowPanelEmptyState(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{Slug: "demo", AppID: "app-1", Status: "active"},
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Workflows") || !strings.Contains(body, "No workflow runs yet.") {
		t.Fatalf("workflow empty state missing\n--- body ---\n%s", body)
	}
}
