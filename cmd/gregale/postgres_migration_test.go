package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresMigrationCheckCLI(t *testing.T) {
	const secret = "migration-fixture-credential"
	t.Setenv("PG_MIGRATION_TEST_URL", "postgres://owner:"+secret+"@source.example.com/source?sslmode=verify-full")
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/capabilities" {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/postgres/databases/"+restoreSwitchDB+"/migration-check" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var src api.ManagedPostgresImportSource
		if err := json.NewDecoder(r.Body).Decode(&src); err != nil || src.Password != secret || src.Database != "source" {
			t.Error("migration inspection did not receive the expected source")
		}
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresMigrationCheck{
			Outcome: "blocked", SourceMajor: 16, TargetMajor: 17, SourceWALLimitBytes: -1,
			Reasons: []string{"source_tables_unsupported"}, UnsupportedTableCount: 1, UnsupportedTables: []string{"public.orders"},
		})
	})
	code, out, stderr := runSwitchCLI(t, server, "postgres", "migrate", "check", restoreSwitchDB, "--source-env", "PG_MIGRATION_TEST_URL")
	if code != 1 || !strings.Contains(out, "public.orders") || !strings.Contains(out, "unlimited") || strings.Contains(out+stderr, secret) {
		t.Fatalf("check exit %d, output %q, stderr %q", code, out, stderr)
	}
}

func TestPostgresLiveMigrationCLI(t *testing.T) {
	const op = "626a63ad-801c-493f-a952-40fd28cd9254"
	const verification = "cbd652d0-e2fb-4e7f-a779-5f04806da1cf"
	const secret = "live-migration-fixture"
	t.Setenv("PG_MIGRATION_TEST_URL", "postgres://owner:"+secret+"@source.example.com/source?sslmode=verify-full")
	base := "/v1/postgres/databases/" + restoreSwitchDB + "/migration"
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/capabilities" {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		progress := api.ManagedPostgresMigrationProgress{OperationID: op, Tables: 2}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == base+"/verify":
			var request api.ManagedPostgresMigrationVerifyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.OperationID != op ||
				request.Source.Password != secret || request.VerificationID != verification || !request.SourceWritesPaused {
				t.Error("verification did not receive the original operation, verification ID and source pause")
			}
			progress.State = "live"
			progress.Verification = &api.ManagedPostgresMigrationVerification{ID: verification, State: "verifying"}
		case r.Method == http.MethodPost && (r.URL.Path == base || r.URL.Path == base+"/stop" || r.URL.Path == base+"/prepare"):
			var request api.ManagedPostgresImportRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.OperationID != op ||
				request.Source.Password != secret || request.Source.Host != "source.example.com" {
				t.Error("migration request did not receive the expected operation and source")
			}
			progress.State = "preparing"
			switch r.URL.Path {
			case base + "/stop":
				progress.State = "stopped"
			case base + "/prepare":
				progress.State = "live"
				progress.Preparation = &api.ManagedPostgresMigrationPreparation{State: "preparing"}
			}
		case r.Method == http.MethodGet && r.URL.Path == base:
			progress.State, progress.TablesReady, progress.ApplyLagBytes = "catching_up", 2, 1024
			progress.Preparation = &api.ManagedPostgresMigrationPreparation{
				State: "blocked", IndexesTotal: 2, IndexesReady: 2, Sequences: 1,
				Blockers: []api.ManagedPostgresMigrationPreparationBlocker{{Code: "source_constraints", Count: 2}},
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(progress)
	})
	for _, tt := range []struct {
		action, state string
	}{
		{"start", "preparing"},
		{"status", "source constraints (2)"},
		{"prepare", "Cutover preparation: preparing"},
		{"verify", "Replay verification: verifying"},
		{"stop", "stopped"},
	} {
		t.Run(tt.action, func(t *testing.T) {
			args := []string{"postgres", "migrate", tt.action, restoreSwitchDB}
			if tt.action != "status" {
				args = append(args, "--request-id", op, "--source-env", "PG_MIGRATION_TEST_URL")
			}
			if tt.action == "verify" {
				args = append(args, "--verification-id", verification, "--source-writes-paused")
			}
			code, out, stderr := runSwitchCLI(t, server, args...)
			if code != 0 || !strings.Contains(out, tt.state) || !strings.Contains(out, op) || strings.Contains(out+stderr, secret) {
				t.Fatalf("command %s failed to report %s safely, exit %d", tt.action, tt.state, code)
			}
		})
	}
}

func TestPostgresMigrationActivateCLI(t *testing.T) {
	const source = "f81ba2ff-5ffc-4a4f-97b5-a7f5f4552256"
	const operation = "626a63ad-801c-493f-a952-40fd28cd9254"
	const verification = "cbd652d0-e2fb-4e7f-a779-5f04806da1cf"
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/capabilities" {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/shop/database-switches/migration" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		want := api.ActivateManagedPostgresMigrationRequest{
			SourceDatabaseID: source, TargetDatabaseID: restoreSwitchDB, OperationID: operation,
			VerificationID: verification, SourceWritesPaused: true, Scope: "staging",
		}
		var req api.ActivateManagedPostgresMigrationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req != want {
			t.Errorf("activation request = %+v, error = %v", req, err)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.DatabaseSwitch{ID: verification, MigrationOperationID: operation, Phase: "staging"})
	})
	code, out, stderr := runSwitchCLI(t, server, "postgres", "migrate", "activate", restoreSwitchDB,
		"--source", source, "--app", "shop", "--env", "staging", "--request-id", operation,
		"--verification-id", verification, "--source-writes-paused", "--no-wait")
	if code != 0 || !strings.Contains(out, verification) {
		t.Fatalf("activation switch was not returned: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
}
