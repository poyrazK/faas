package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func TestPostgresExtensionsEnableAndCheck(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := "/v1/postgres/databases/" + restoreSwitchDB + "/extensions"
		version := api.ManagedPostgresExtensionVersion{Name: pgextension.VectorName, Version: pgextension.VectorVersion}
		report := api.ManagedPostgresExtensions{Installed: []api.ManagedPostgresExtensionVersion{version}, Available: []api.ManagedPostgresExtensionVersion{}}
		switch {
		case r.URL.Path == "/v1/capabilities":
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
		case r.URL.Path == "/v1/postgres/databases" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: restoreSwitchDB, Name: "orders"}}})
		case r.URL.Path == path+"/vector" && r.Method == http.MethodPut:
			_ = json.NewEncoder(w).Encode(report)
		case r.URL.Path == path && r.Method == http.MethodGet && r.URL.Query().Get("target_major") == "18":
			report.IncompatibleExtensions = []api.ManagedPostgresExtensionVersion{version}
			_ = json.NewEncoder(w).Encode(report)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	if code, out, stderr := runSwitchCLI(t, handler, "postgres", "extensions", "enable", "orders", "vector"); code != 0 || !strings.Contains(out, "vector "+pgextension.VectorVersion) {
		t.Fatalf("enable exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	if code, out, stderr := runSwitchCLI(t, handler, "postgres", "upgrade", "--check", "orders", "--major", "18"); code != 1 || !strings.Contains(out, "Incompatible with PostgreSQL 18") {
		t.Fatalf("check exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
}
