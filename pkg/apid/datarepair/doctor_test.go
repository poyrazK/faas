package datarepair

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestRepairDoctorAuthenticatedReader(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	if _, err := pool.Exec(t.Context(), core.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE TABLE products(id int PRIMARY KEY,category text NOT NULL);
SELECT gregale_repair.enroll('products',ARRAY['category']); INSERT INTO products VALUES(1,'original')`); err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	engine := &core.Engine{Pool: pool, CheckSQL: "SELECT 1=1"}
	token := strings.Repeat("r", 32)
	handler := (&Handler{Engine: engine, Authenticate: Bearer(token, Principal{Scope: scope, Actor: "reader"})}).Routes()
	server := httptest.NewServer(handler)
	defer server.Close()
	remote, err := NewRemote(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	report, err := remote.Doctor(t.Context(), scope, "public.products")
	if err != nil || !report.Valid() || report.Status != "ready" {
		t.Fatalf("reader could not inspect readiness: %+v %v", report, err)
	}
	for _, tc := range []struct {
		query, token string
		status       int
	}{
		{"table=public.products", "", 401}, {"table=public.products", token, 200},
		{"table=public.unknown", token, 200}, {"table=products", token, 400},
		{"table=public.products&table=public.other", token, 400}, {"table=public.products&environment=production", token, 400},
		{"table=public.products&url=https://untrusted.example", token, 400}, {"table=public.products&checks=SELECT+true", token, 400},
		{"table=public.products&scope=forged", token, 400}, {"", token, 400},
		{"table=public.products&profile=capture", token, 400}, {"table=public.products&database-env=WRITER_DATABASE_URL", token, 400},
	} {
		r := httptest.NewRequest("GET", "/v1/data-repair/doctor?"+tc.query, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), `"category"`) {
			t.Fatalf("doctor authentication/query/payload guard: %+v %d %s", tc, w.Code, w.Body.String())
		}
	}
	other := scope
	other.PlatformTenantID = uuid.NewString()
	if _, err := remote.Doctor(t.Context(), other, "public.products"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatal("remote accepted a different scope", err)
	}
	var category string
	if err := pool.QueryRow(t.Context(), `SELECT category FROM products WHERE id=1`).Scan(&category); err != nil || category != "original" {
		t.Fatal("doctor changed application data", category, err)
	}
}

func TestRepairDoctorRemoteAndBindingGuards(t *testing.T) {
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	redirects := 0
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirects++ }))
	defer destination.Close()
	for _, mode := range []string{"scope", "table", "aggregate", "policy", "duplicate", "capture_profile", "oversized", "redirect", "old_executor", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/data-repair/doctor" || r.URL.Query().Get("table") != "public.products" ||
					r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) || r.ContentLength > 0 || r.Header.Get("Idempotency-Key") != "" {
					t.Error("doctor supplied incorrect method, identity or authority")
				}
				report := core.UnavailableDoctor(scope, "public.products")
				switch mode {
				case "scope":
					report.Scope.AppID = uuid.NewString()
				case "table":
					report.Table = "public.other"
				case "aggregate":
					report.Status = "ready"
				case "policy":
					report.Policy = core.DoctorPolicy{Mode: "named", Digest: strings.Repeat("a", 64), Count: 1}
				case "duplicate":
					report.Findings[1] = report.Findings[0]
				case "capture_profile":
					report.Profile = "capture"
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", api.DataRepairDoctorMaxResponseBytes+1)))
					return
				case "redirect":
					http.Redirect(w, r, destination.URL, 307)
					return
				case "old_executor":
					w.WriteHeader(404)
					return
				case "unavailable":
					w.WriteHeader(503)
					return
				}
				_ = json.NewEncoder(w).Encode(report)
			}))
			defer server.Close()
			remote, err := NewRemote(server.URL, strings.Repeat("x", 32))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := remote.Doctor(t.Context(), scope, "public.products"); !errors.Is(err, core.ErrUnavailable) {
				t.Fatal("remote trusted invalid report", err)
			}
			r := boundDoctor(t.Context(), Binding{Scope: scope, Executor: remote}, "public.products")
			if !r.Valid() || r.Status != "unknown" || len(r.Findings) != 10 || r.Findings[0].Code != "executor_unavailable" || r.Findings[9].Code != "binding_configured" {
				t.Fatalf("unavailable executor became ready for a bound source: %+v", r)
			}
		})
	}
	if redirects != 0 {
		t.Fatal("doctor followed a credential redirect")
	}
	r := boundDoctor(t.Context(), Binding{Scope: scope}, "public.products")
	if !r.Valid() || r.Status != "unknown" {
		t.Fatal("executor without doctor support became ready", r)
	}
}
