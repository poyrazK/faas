package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderManagedPostgresNeverIncludesCredentialMaterial(t *testing.T) {
	out := httptest.NewRecorder()
	page := Page{
		Title:   "Managed PostgreSQL",
		Body:    "postgres",
		Account: &AccountView{Email: "alice@example.com", Plan: "pro"},
		Data: ManagedPostgresData{Available: true, Databases: []ManagedPostgresDatabaseItem{{
			ID: "db-1", Name: "orders", Region: "eu", PostgresMajor: 16,
			ServiceClass: "burstable", Availability: "single_zone", ScaleToZero: true,
			StorageLimitLabel: "50 GiB", RestoreWindowSeconds: 604800, State: "ready",
			Bindings: []ManagedPostgresBindingItem{{ID: "binding-1", AppID: "app-1", Scope: "production", EnvironmentKey: "DATABASE_URL", Access: "read_write", State: "ready", CredentialGeneration: 1}},
		}}},
	}
	if err := Render(out, slog.Default(), "nonce", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := out.Body.String()
	for _, want := range []string{"orders", "db-1", "DATABASE_URL", "ready", "50 GiB"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	for _, forbidden := range []string{"postgres-password", "connection_url", "postgres://", "should-never-be-rendered"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body contains forbidden credential material %q", forbidden)
		}
	}
}

// A scheduled delete (ADR-20261029174950) shows when it becomes final and
// how to undo or finish it; a database with none shows neither.
func TestRenderManagedPostgresScheduledDelete(t *testing.T) {
	for _, tc := range []struct {
		name            string
		restorableUntil string
		want, forbidden []string
	}{
		{name: "scheduled", restorableUntil: "2026-11-06T12:00:00Z",
			want: []string{"Scheduled for deletion", "Undo before 2026-11-06T12:00:00Z", "gregale postgres recover orders", "gregale postgres delete orders --purge"}},
		{name: "ready", forbidden: []string{"Scheduled for deletion", "gregale postgres recover orders"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := httptest.NewRecorder()
			page := Page{
				Title: "Managed PostgreSQL", Body: "postgres",
				Account: &AccountView{Email: "alice@example.com", Plan: "pro"},
				Data: ManagedPostgresData{Available: true, Databases: []ManagedPostgresDatabaseItem{{
					ID: "db-1", Name: "orders", State: "deleting", RestorableUntil: tc.restorableUntil,
				}}},
			}
			if err := Render(out, slog.Default(), "nonce", page); err != nil {
				t.Fatalf("render: %v", err)
			}
			body := out.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q", want)
				}
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(body, forbidden) {
					t.Errorf("body contains %q", forbidden)
				}
			}
		})
	}
}
