package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

// ADR-20261024150500: the account deletion form offers the managed PostgreSQL
// checkbox only to accounts in the launch allowlist.
func TestRender_AccountDeleteManagedPostgresCheckbox(t *testing.T) {
	for _, shown := range []bool{false, true} {
		rec := httptest.NewRecorder()
		page := dashboard.Page{
			Title:   "Account",
			Body:    "account",
			Account: &dashboard.AccountView{ID: "acct-1", Email: "jane@example.test", Plan: "pro"},
			Data:    dashboard.AccountData{ShowDelete: true, ShowDeleteManagedPostgres: shown},
		}
		if err := dashboard.Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonceSmokeTest1234567ab", page); err != nil {
			t.Fatalf("render: %v", err)
		}
		if got := strings.Contains(rec.Body.String(), `name="include_managed_postgres_databases"`); got != shown {
			t.Errorf("checkbox rendered = %v, want %v", got, shown)
		}
	}
}
