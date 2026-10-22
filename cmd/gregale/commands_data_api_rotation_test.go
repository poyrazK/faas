package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const rotationTestBinding = "00000000-0000-4000-8000-000000000001"

func rotationOwnerFixture() main.DataAPIRotationOwnerReport {
	paused, members := false, 1
	report := main.DataAPIRotationOwnerReport{Version: 1, Kind: "data_api_rotation_preflight", BindingID: rotationTestBinding, Ready: true, Complete: true, Paused: &paused, MemberCount: &members}
	for code := range *main.DataAPIRotationActions {
		report.Checks = append(report.Checks, main.DataAPIRotationCheck{Code: code, Status: "passed", NextAction: "postgres://private-owner:secret@example/database"})
	}
	slices.SortFunc(report.Checks, func(a, b main.DataAPIRotationCheck) int { return strings.Compare(a.Code, b.Code) })
	return report
}

// This child exits directly so the Go test runner cannot contaminate stdout.
func TestDataAPIRotationOwnerHelper(t *testing.T) {
	mode := os.Getenv("GREGALE_ROTATION_TEST_HELPER")
	if mode == "" {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, "postgres://private-owner:secret@example/database")
	report, exit := rotationOwnerFixture(), 0
	switch mode {
	case "blocked", "unknown":
		report.Ready, exit = false, 1
		report.Checks[0].Status = mode
		if mode == "unknown" {
			report.Complete = false
		}
	case "foreign":
		report.BindingID = "00000000-0000-4000-8000-000000000002"
	case "duplicate":
		report.Checks[1] = report.Checks[0]
	case "contradiction":
		report.Ready = false
	case "oversized":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("private-owner", api.DataAPIRotationReportMaxBytes))
		os.Exit(0)
	case "trailing-oversized":
		report.Ready, report.Checks[0].Status = false, "blocked"
		_ = json.NewEncoder(os.Stdout).Encode(report)
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("private-owner", api.DataAPIRotationReportMaxBytes))
		os.Exit(1)
	case "malformed":
		_, _ = fmt.Fprint(os.Stdout, "private-owner-secret")
		os.Exit(1)
	case "crash":
		os.Exit(2)
	case "timeout":
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
	os.Exit(exit)
}

func rotationCommandConfig(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv("GREGALE_ROTATION_TEST_HELPER", mode)
	directory := t.TempDir()
	path := filepath.Join(directory, "owner-check.json")
	content, err := json.Marshal(main.DataAPISyncCommand{Command: []string{os.Args[0], "-test.run=^TestDataAPIRotationOwnerHelper$"}, Directory: "."})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func rotationInventoryFixture() (api.ManagedPostgresBinding, api.AppBindingInventory) {
	binding := api.ManagedPostgresBinding{ID: rotationTestBinding, AppID: "app", EnvironmentKey: "DATABASE", Scope: "production", Access: "data_api", State: "ready", CredentialGeneration: 2}
	generation, pending := binding.CredentialGeneration, false
	inventory := api.AppBindingInventory{App: "notes", Scope: "production", Complete: true, GeneratedAt: time.Now().UTC(), Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypePostgres, Binding: "DATABASE", Scope: "production", Access: "data_api", State: "ready", CredentialGeneration: &generation, RotationPending: &pending}}}
	return binding, inventory
}

func TestDataAPIRotationPreflightReadOnlyAndSanitized(t *testing.T) {
	for _, mode := range []string{"ready", "pending", "partial", "changed", "blocked", "unknown", "foreign", "duplicate", "contradiction", "oversized", "trailing-oversized", "malformed", "crash"} {
		t.Run(mode, func(t *testing.T) {
			config := rotationCommandConfig(t, mode)
			binding, inventory := rotationInventoryFixture()
			switch mode {
			case "pending":
				binding.RotationPending, *inventory.Bindings[0].RotationPending = true, true
			case "partial":
				inventory.Complete = false
			case "changed":
				*inventory.Bindings[0].CredentialGeneration = 3
			}
			var reads []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("preflight attempted mutation: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				reads = append(reads, r.URL.Path)
				switch r.URL.Path {
				case "/v1/apps/notes":
					main.WriteJSONTest(w, api.AppResponse{ID: "app", Slug: "notes"})
				case "/v1/postgres/bindings/" + rotationTestBinding:
					main.WriteJSONTest(w, binding)
				case "/v1/apps/notes/bindings":
					if r.URL.Query().Get("scope") != "production" {
						t.Error("missing exact binding scope")
					}
					main.WriteJSONTest(w, inventory)
				default:
					t.Errorf("unexpected API read: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "private-account-token")
			t.Setenv("GREGALE_DATA_API_ACCESS_TOKEN", "")
			t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", "")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run([]string{"--json", "data-api", "rotation", "preflight", "notes", "--binding", rotationTestBinding, "--owner-check", config})
			if (code == 0) != (mode == "ready") {
				t.Fatalf("exit=%d output=%s err=%s", code, out.String(), stderr())
			}
			if len(reads) != 3 {
				t.Fatalf("expected only app, binding and inventory reads: %v", reads)
			}
			if slices.Contains([]string{"ready", "pending", "partial", "changed", "blocked", "unknown"}, mode) {
				var result main.DataAPIRotationReport
				if err := json.Unmarshal([]byte(out.String()), &result); err != nil || result.Ready == nil || *result.Ready != (mode == "ready") || result.Owner == nil {
					t.Fatalf("unexpected report: %s (%v)", out.String(), err)
				}
			} else if strings.Contains(out.String(), `"owner"`) {
				t.Fatal("invalid owner output reached the report")
			}
			for _, secret := range []string{"private-owner", "secret@example", "private-account-token"} {
				if strings.Contains(out.String()+stderr(), secret) {
					t.Fatal("owner command output leaked")
				}
			}
		})
	}
}

func TestDataAPIRotationOwnerDeadline(t *testing.T) {
	path := rotationCommandConfig(t, "timeout")
	command, err := main.LoadDataAPIRotationCommand(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := main.RunDataAPIRotationOwnerCheck(ctx, command, rotationTestBinding); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline was not preserved: %v", err)
	}
}

func TestDataAPIRotationOutputBoundsPipeCopies(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "owner-output")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(strings.Repeat("x", api.DataAPIRotationReportMaxBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var output main.DataAPIRotationOutput
	if _, err := io.Copy(&output, file); err == nil || !(*output.Exceeded()) || (*output.Buffer()).Len() > api.DataAPIRotationReportMaxBytes {
		t.Fatalf("file-copy optimization bypassed the output bound: size=%d err=%v", (*output.Buffer()).Len(), err)
	}
}

func TestDataAPIRotationRolloutDoesNotConflateHandoffWithRetirement(t *testing.T) {
	for _, name := range []string{"idle", "unavailable", "pending", "completed", "retiring", "failed", "incomplete", "missing", "changed", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			binding, inventory := rotationInventoryFixture()
			item := &inventory.Bindings[0]
			binding.RotationPending = name != "idle" && name != "unavailable"
			*item.RotationPending = binding.RotationPending
			switch name {
			case "unavailable":
				binding.State, item.State = "deleting", "deleting"
			case "completed", "failed":
				item.Refresh = &api.BindingRefresh{Status: name, WakeID: "wake"}
			case "retiring":
				binding.State, item.State = "retiring", "retiring"
			case "incomplete":
				inventory.Complete = false
			case "missing":
				inventory.Bindings = nil
			case "changed":
				*item.CredentialGeneration = 3
			case "duplicate":
				inventory.Bindings = append(inventory.Bindings, *item)
			}
			result := main.DescribeDataAPIRotationRollout("notes", binding, inventory)
			want := map[string]string{"idle": "idle", "unavailable": "binding_unavailable", "pending": "rollout_pending", "completed": "cleanup_pending", "retiring": "retiring", "failed": "rollout_failed", "incomplete": "unknown", "missing": "unknown", "changed": "changed_during_read", "duplicate": "unknown"}[name]
			if result.State != want || result.Complete != !slices.Contains([]string{"incomplete", "missing", "changed", "duplicate"}, name) || (binding.RotationPending && result.Retirement == "no_previous_generation_pending") {
				t.Fatalf("incorrect rollout interpretation: %+v", result)
			}
		})
	}
}

func TestDataAPIRotationStatusAndHostedDiagnostics(t *testing.T) {
	for _, command := range []string{"status", "diagnostics", "diagnostics-unavailable", "foreign"} {
		t.Run(command, func(t *testing.T) {
			binding, inventory := rotationInventoryFixture()
			binding.RotationPending, *inventory.Bindings[0].RotationPending = true, true
			inventory.Bindings[0].Refresh = &api.BindingRefresh{Status: "completed"}
			if command == "foreign" {
				binding.AppID = "another-app"
			}
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != "GET" {
					t.Errorf("unexpected write: %s", r.Method)
				}
				switch r.URL.Path {
				case "/v1/apps/notes":
					main.WriteJSONTest(w, api.AppResponse{ID: "app", Slug: "notes"})
				case "/v1/postgres/bindings/" + rotationTestBinding:
					main.WriteJSONTest(w, binding)
				case "/v1/apps/notes/bindings":
					if strings.HasPrefix(command, "diagnostics") && r.URL.Query().Get("deployment_id") != "deploy" {
						t.Error("diagnostics lost deployment selection")
					}
					if command == "diagnostics-unavailable" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					main.WriteJSONTest(w, inventory)
				case "/v1/apps/notes/logs":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\nevent: end\ndata: {}\n\n", dataAPIDiagnosticEnvelope(t, dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil)))
				default:
					t.Errorf("unexpected API call: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "account-token")
			t.Setenv("GREGALE_DATA_API_ACCESS_TOKEN", "")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			args := []string{"--json", "data-api", "rotation", "status", "notes", "--binding", rotationTestBinding}
			if strings.HasPrefix(command, "diagnostics") {
				args = []string{"--json", "data-api", "diagnostics", "notes", "--rotation-binding", rotationTestBinding, "--deployment", "deploy"}
			}
			code := main.Run(args)
			if (code == 0) != (command != "foreign" && command != "diagnostics-unavailable") {
				t.Fatalf("exit=%d out=%s err=%s", code, out.String(), stderr())
			}
			if command == "foreign" {
				if reads != 2 {
					t.Fatal("foreign binding reached inventory or owner execution")
				}
				return
			}
			if command == "diagnostics-unavailable" {
				var report main.DataAPIDiagnosticsReport
				if err := json.Unmarshal([]byte(out.String()), &report); err != nil || !report.Partial || !slices.Contains(report.PartialReasons, "rotation_inventory_unavailable") || report.Summary.Requests != 1 || reads != 4 {
					t.Fatalf("metadata failure lost log evidence: %s (%v)", out.String(), err)
				}
				return
			}
			if !strings.Contains(out.String(), `"retirement": "pending"`) {
				t.Fatalf("handoff was confused with retirement: %s", out.String())
			}
		})
	}
}

func TestDataAPIRotationRejectsOfflineAndInvalidConfig(t *testing.T) {
	out, _, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"--json", "data-api", "diagnostics", "--file", "offline.ndjson", "--rotation-binding", rotationTestBinding}); code == 0 || strings.Contains(out.String(), `"rotation"`) {
		t.Fatal("offline diagnostics accepted a hosted read")
	}
	path := filepath.Join(t.TempDir(), "check.json")
	for _, content := range []string{`{"command":["node"],"unknown":"private-owner"}`, `{"command":[]} {}`, strings.Repeat(" ", api.DataAPIRotationConfigMaxBytes+1)} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := main.LoadDataAPIRotationCommand(path); err == nil || strings.Contains(err.Error(), "private-owner") {
			t.Fatalf("invalid owner configuration accepted or exposed: %v", err)
		}
	}
}
