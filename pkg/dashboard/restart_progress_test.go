package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestRestartProgressPageDistinguishesCompletionFromHealth(t *testing.T) {
	row := api.RuntimeConfigRestartStatusResponse{WakeID: "restart-id", Status: "completed", RequestedAt: time.Now().UTC()}
	now := time.Now().UTC()
	row.CompletedAt = &now
	w := httptest.NewRecorder()
	page := dashboard.Page{Title: "Restart progress", Body: "restart_progress", Data: dashboard.AppRestartProgressData{App: dashboard.AppListItem{Slug: "demo"}, WakeID: row.WakeID, Status: &row}}
	if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", page); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Restart processing is complete", "health has not been verified", "restart status --wake-id restart-id --wait", "View logs", "View current application health"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), `http-equiv="refresh"`) {
		t.Fatal("terminal progress keeps refreshing")
	}
}
