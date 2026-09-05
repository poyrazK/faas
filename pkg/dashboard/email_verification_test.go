package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmailVerificationBannerPersistsAcrossDashboardPages(t *testing.T) {
	page := Page{
		Title: "Overview",
		Body:  "index",
		Account: &AccountView{
			Email:                      "unverified@example.com",
			Plan:                       "free",
			EmailVerificationGraceEnds: "2026-10-05",
		},
	}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "", page); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{"Verify your email", "unverified@example.com", "2026-10-05"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}
