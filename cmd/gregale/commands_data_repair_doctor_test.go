package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func TestDataRepairDoctorCLIReadOnlyAndScoped(t *testing.T) {
	scope := datarepair.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production", PlatformTenantID: uuid.NewString()}
	report := datarepair.UnavailableDoctor(scope, "public.products")
	var calls int
	var gotPath, gotToken, gotCustomer string
	oversized := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.ContentLength > 0 || r.Header.Get("Idempotency-Key") != "" || r.URL.Query().Get("table") != "public.products" {
			t.Error("doctor attempted a mutation or changed table")
		}
		gotPath, gotToken, gotCustomer = r.URL.Path, r.Header.Get("Authorization"), r.URL.Query().Get("customer")
		if oversized {
			_, _ = w.Write([]byte(strings.Repeat("x", api.DataRepairDoctorMaxResponseBytes+1)))
			return
		}
		_ = json.NewEncoder(w).Encode(report)
	}))
	defer server.Close()
	t.Setenv("GREGALE_REPAIR_TOKEN", "executor-reader")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "hosted-reader")
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	defer func() { osStdout = old }()
	if cmdDataRepair([]string{"doctor", "--table", "public.products", "--url", server.URL}) != 1 || gotPath != "/v1/data-repair/doctor" || gotToken != "Bearer executor-reader" {
		t.Fatal("standalone doctor lost its fixed origin, authority or unknown exit status")
	}
	var got datarepair.DoctorReport
	if json.Unmarshal(out.Bytes(), &got) != nil || !got.Valid() || got.Status != "unknown" {
		t.Fatal("doctor did not print the structured unknown report")
	}
	out.Reset()
	report.BoundDoctor()
	if cmdDataRepair([]string{"doctor", "--table", "public.products", "--app", "products", "--environment", scope.Environment, "--customer", scope.PlatformTenantID}) != 1 ||
		gotPath != "/v1/apps/products/data-repair/doctor" || gotToken != "Bearer hosted-reader" || gotCustomer != scope.PlatformTenantID || out.Len() == 0 {
		t.Fatal("hosted doctor lost exact scope or reader authority")
	}
	before := calls
	for _, args := range [][]string{
		{"doctor"}, {"doctor", "--table", "products"}, {"doctor", "--table", "public.products", "--customer", scope.PlatformTenantID},
		{"doctor", "--table", "public.products", "--app", "products", "--url", server.URL},
		{"doctor", "--table", "public.products", "--fix"}, {"doctor", "--table", "public.products", "--checks-file", "untrusted.sql"},
		{"doctor", "--table", "public.products", "--database-env", "WRITER_DB"},
		{"doctor", "--table", "public.products", "--profile", "unknown"},
		{"doctor", "--table", "public.products", "--profile", "capture", "--app", "products"},
		{"doctor", "--table", "public.products", "--profile", "capture", "--url", server.URL},
	} {
		if cmdDataRepair(args) != 1 {
			t.Fatal("accepted invalid query or mutation authority", args)
		}
	}
	if calls != before {
		t.Fatal("invalid doctor flags contacted an API")
	}
	for _, mode := range []string{"scope", "table", "aggregate"} {
		t.Run(mode, func(t *testing.T) {
			report = datarepair.UnavailableDoctor(scope, "public.products")
			report.BoundDoctor()
			switch mode {
			case "scope":
				report.Scope.PlatformTenantID = uuid.NewString()
			case "table":
				report.Table = "public.other"
			case "aggregate":
				report.Status = "ready"
			}
			out.Reset()
			if cmdDataRepair([]string{"doctor", "--table", "public.products", "--app", "products", "--customer", scope.PlatformTenantID}) != 1 || out.Len() != 0 {
				t.Fatal("invalid report became printable readiness", strings.TrimSpace(out.String()))
			}
		})
	}
	oversized = true
	out.Reset()
	if cmdDataRepair([]string{"doctor", "--table", "public.products", "--url", server.URL}) != 1 || out.Len() != 0 {
		t.Fatal("oversized diagnostic response was printed")
	}
}
