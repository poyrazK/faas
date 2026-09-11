package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestRender_AppDetail_GitHubConnection(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{Slug: "demo", AppID: "demo-uuid", Status: "active"},
			GitHubConnection: &dashboard.GitHubConnectionView{
				Available:                    true,
				State:                        "bound",
				Health:                       "healthy",
				Connected:                    true,
				GitHubLogin:                  "alice",
				RepoFullName:                 "acme/api",
				ProductionBranch:             "main",
				LastReconciledAt:             "2026-09-12 10:00:00 UTC",
				LastReconcileRepositoryCount: 3,
				CSRFToken:                    "csrf-token",
			},
		},
	}
	if err := dashboard.Render(rec, log, "github-nonce", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"GitHub connection",
		"acme/api",
		"main",
		"alice",
		"Sync GitHub access",
		"Disconnect",
		`name="csrf_token" value="csrf-token"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
	if strings.Contains(body, "installation_token") || strings.Contains(body, "access_token") {
		t.Errorf("rendered GitHub connection leaked a credential field")
	}
}
