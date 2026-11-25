package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const validWriteObservation = `{"version":1,"role":{"name":"app_role","database":"db","superuser":false,"bypass_rls":false},"relation":{"name":"notes","kind":"r","rls":true,"force_rls":false,"owner_member":false,"row_security_active":true,"schema_usage":true,"can_select":true,"can_insert":true,"can_update":true,"can_delete":true},"columns":[{"name":"id","generated":false,"identity":true,"can_select":true,"can_insert":false,"can_update":false}],"policies":[{"name":"own","command":"*","permissive":true,"applies_to_role":true,"has_using":true,"has_check":true}],"triggers":[],"transport":{"encrypted":true,"peer_authorized":true}}`

type writeHostedFixture struct {
	app          api.AppResponse
	db           api.ManagedPostgresDatabase
	binding      api.ManagedPostgresBinding
	review       api.AppReleaseReview
	task         api.AppTaskResponse
	request      api.CreateAppTaskRequest
	observations int
	changeAfter  bool
}

func (f *writeHostedFixture) GetApp(context.Context, string) (api.AppResponse, error) {
	return f.app, nil
}
func (f *writeHostedFixture) GetManagedPostgresDatabase(context.Context, string) (api.ManagedPostgresDatabase, error) {
	return f.db, nil
}
func (f *writeHostedFixture) GetManagedPostgresBinding(context.Context, string) (api.ManagedPostgresBinding, error) {
	f.observations++
	b := f.binding
	if f.changeAfter && f.observations > 1 {
		b.CredentialGeneration++
	}
	return b, nil
}
func (f *writeHostedFixture) GetAppReleaseReview(context.Context, string) (api.AppReleaseReview, error) {
	return f.review, nil
}
func (f *writeHostedFixture) CreateAppTask(_ context.Context, _ string, req api.CreateAppTaskRequest) (api.AppTaskResponse, error) {
	f.request = req
	task := f.task
	task.Command = req.Command
	return task, nil
}
func (f *writeHostedFixture) GetAppTask(context.Context, string, string) (api.AppTaskResponse, error) {
	return f.task, nil
}
func newWriteHostedFixture() *writeHostedFixture {
	zero := 0
	return &writeHostedFixture{
		app: api.AppResponse{ID: "app-1", Slug: "notes"}, db: api.ManagedPostgresDatabase{ID: "db-1", State: "ready"},
		binding: api.ManagedPostgresBinding{ID: "binding-1", AppID: "app-1", DatabaseID: "db-1", Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: "data_api", State: "ready", CredentialGeneration: 3},
		review:  api.AppReleaseReview{AppID: "app-1", Scope: api.DefaultEnvScope, LatestDeploymentID: "dep-1", ServingDeploymentIDs: []string{"dep-1"}, ConfigurationHash: strings.Repeat("a", 64)},
		task:    api.AppTaskResponse{ID: "task-1", AppID: "app-1", DeploymentID: "dep-1", DeploymentScope: api.DefaultEnvScope, Status: api.AppTaskStatusSucceeded, ExitCode: &zero, StdoutTail: validWriteObservation},
	}
}

func TestDataAPIWriteHostedInspectionPinsResourcesAndReportsLimits(t *testing.T) {
	f := newWriteHostedFixture()
	scope, _ := dataAPIWriteScope("api", "notes", "insert,update,delete")
	report, err := inspectDataAPIWriteHosted(context.Background(), f, "notes", "db-1", "binding-1", scope)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "review_required" || report.Authorization != "not_tested" || report.BindingDelivery != "not_independently_verified" || report.BindingIdentity.Status != "unknown" || report.BindingIdentity.Reason != "credential_and_database_target_not_independently_verified" || report.CredentialGeneration != 3 || len(report.Findings) != 0 || f.observations != 2 {
		t.Fatalf("unexpected report %+v", report)
	}
	if _, problem := f.request.Resolve(); problem != nil {
		t.Fatalf("inspection request is invalid: %v", problem)
	}
	if len(f.request.Command) != 4 || f.request.Command[0] != "node" || f.request.Command[1] != "-e" || f.request.Command[3] != "notes" || len(f.request.Command[2]) > api.AppTaskMaxCommandArgBytes || f.request.TimeoutSeconds != 60 || f.request.MaxOutputBytes != 65536 {
		t.Fatalf("invalid task request %+v", f.request)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "DATABASE_URL") && strings.Contains(string(encoded), "password") {
		t.Fatal("report contains credential material")
	}
}

func TestDataAPIWriteTransportNeverProvesBindingIdentity(t *testing.T) {
	scope, _ := dataAPIWriteScope("api", "notes", "insert")
	for name, transport := range map[string]string{
		"authorized_tls":   `{"encrypted":true,"peer_authorized":true}`,
		"unauthorized_tls": `{"encrypted":true,"peer_authorized":false}`,
		"no_tls":           `{"encrypted":false,"peer_authorized":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newWriteHostedFixture()
			f.task.StdoutTail = strings.Replace(validWriteObservation, `{"encrypted":true,"peer_authorized":true}`, transport, 1)
			report, err := inspectDataAPIWriteHosted(context.Background(), f, "notes", "db-1", "binding-1", scope)
			if err != nil || report.BindingIdentity.Status != "unknown" || report.Status != "review_required" {
				t.Fatalf("unexpected identity conclusion: report=%+v err=%v", report, err)
			}
		})
	}
}

func TestDataAPIWriteHostedRejectsChangedOrUntrustedEvidence(t *testing.T) {
	scope, _ := dataAPIWriteScope("api", "notes", "insert")
	for name, change := range map[string]func(*writeHostedFixture){
		"binding_generation_changed": func(f *writeHostedFixture) { f.changeAfter = true },
		"read_only_binding":          func(f *writeHostedFixture) { f.binding.Access = "read_only" },
		"rotating_binding":           func(f *writeHostedFixture) { f.binding.RotationPending = true },
		"wrong_app":                  func(f *writeHostedFixture) { f.binding.AppID = "other" },
		"multiple_deployments": func(f *writeHostedFixture) {
			f.review.ServingDeploymentIDs = append(f.review.ServingDeploymentIDs, "other")
		},
		"wrong_task_deployment": func(f *writeHostedFixture) { f.task.DeploymentID = "other" },
		"truncated_output":      func(f *writeHostedFixture) { f.task.OutputTruncated = true },
		"missing_exit":          func(f *writeHostedFixture) { f.task.ExitCode = nil },
		"invalid_output":        func(f *writeHostedFixture) { f.task.StdoutTail = `{"version":1}` },
		"different_table": func(f *writeHostedFixture) {
			f.task.StdoutTail = strings.Replace(validWriteObservation, `"name":"notes"`, `"name":"other"`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newWriteHostedFixture()
			change(f)
			if _, err := inspectDataAPIWriteHosted(context.Background(), f, "notes", "db-1", "binding-1", scope); err == nil {
				t.Fatal("accepted unsafe inspection")
			}
		})
	}
}

func TestDataAPIWriteHostedDistinguishesSelectionMismatchFromUnknown(t *testing.T) {
	scope, _ := dataAPIWriteScope("api", "notes", "insert")
	for name, tc := range map[string]struct {
		change func(*writeHostedFixture)
		want   string
	}{
		"wrong_app":        {func(f *writeHostedFixture) { f.binding.AppID = "other" }, "binding_selection_mismatch"},
		"wrong_database":   {func(f *writeHostedFixture) { f.binding.DatabaseID = "other" }, "binding_selection_mismatch"},
		"missing_app":      {func(f *writeHostedFixture) { f.binding.AppID = "" }, "binding_or_deployment_unavailable"},
		"missing_database": {func(f *writeHostedFixture) { f.binding.DatabaseID = "" }, "binding_or_deployment_unavailable"},
		"wrong_record":     {func(f *writeHostedFixture) { f.binding.ID = "other" }, "binding_or_deployment_unavailable"},
		"rotation":         {func(f *writeHostedFixture) { f.binding.RotationPending = true }, "binding_or_deployment_unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newWriteHostedFixture()
			tc.change(f)
			_, err := inspectDataAPIWriteHosted(context.Background(), f, "notes", "db-1", "binding-1", scope)
			var inspectionError dataAPIWriteInspectionError
			if !errors.As(err, &inspectionError) || inspectionError.code != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
			if len(f.request.Command) != 0 {
				t.Fatal("started inspection for an unqualified binding")
			}
		})
	}
}

func TestDataAPIWriteObservationRejectsMissingFieldsAndFlagsRisks(t *testing.T) {
	for _, input := range []string{strings.Replace(validWriteObservation, `"superuser":false,`, "", 1), strings.Replace(validWriteObservation, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(validWriteObservation, `"triggers":[]`, `"triggers":null`, 1), strings.Replace(validWriteObservation, `"transport":{"encrypted":true,"peer_authorized":true}`, `"transport":{"encrypted":true,"peer_authorized":null}`, 1), strings.Replace(validWriteObservation, `"transport":{"encrypted":true,"peer_authorized":true}`, `"transport":{"encrypted":false,"peer_authorized":true}`, 1), validWriteObservation + `{}`} {
		if _, err := parseDataAPIWriteObservation(input, "notes"); err == nil {
			t.Fatalf("accepted invalid observation %s", input)
		}
	}
	input := strings.Replace(validWriteObservation, `"bypass_rls":false`, `"bypass_rls":true`, 1)
	input = strings.Replace(input, `"can_insert":true`, `"can_insert":false`, 1)
	input = strings.Replace(input, `"applies_to_role":true`, `"applies_to_role":false`, 1)
	observation, err := parseDataAPIWriteObservation(input, "notes")
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := dataAPIWriteScope("api", "notes", "insert")
	got := strings.Join(dataAPIWriteFindings(observation, scope), ",")
	for _, want := range []string{"role_bypasses_rls", "insert_table_grant_missing_review_columns", "insert_applicable_policy_not_observed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestDataAPIWriteReadinessCLIInspectsSelectedBinding(t *testing.T) {
	for _, scenario := range []string{"observed", "binding_changed", "wrong_app", "missing_app"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newWriteHostedFixture()
			if scenario == "wrong_app" {
				fixture.binding.AppID = "other"
			} else if scenario == "missing_app" {
				fixture.binding.AppID = ""
			}
			var bindingReads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer owner-test-token" {
					t.Error("missing management authentication")
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /v1/postgres/databases":
					writeJSONTest(w, api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{fixture.db}})
				case "GET /v1/postgres/databases/db-1":
					writeJSONTest(w, fixture.db)
				case "GET /v1/apps/notes":
					writeJSONTest(w, fixture.app)
				case "GET /v1/apps/notes/release-review":
					writeJSONTest(w, fixture.review)
				case "GET /v1/postgres/bindings/binding-1":
					reads := bindingReads.Add(1)
					binding := fixture.binding
					if scenario == "binding_changed" && reads > 1 {
						binding.CredentialGeneration++
					}
					writeJSONTest(w, binding)
				case "POST /v1/apps/notes/tasks":
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					task := fixture.task
					task.Command = request.Command
					writeJSONTest(w, task)
				case "POST /v1/apps/notes/data-api-inspections":
					if scenario != "binding_changed" {
						w.WriteHeader(http.StatusNotFound) // older control plane
						return
					}
					var observation map[string]json.RawMessage
					if err := json.Unmarshal([]byte(validWriteObservation), &observation); err != nil {
						t.Error(err)
						return
					}
					delete(observation, "transport")
					raw, err := json.Marshal(observation)
					if err != nil {
						t.Error(err)
						return
					}
					writeJSONTest(w, api.DataAPIInspectionResponse{TaskID: "task-1", AppID: "app-1", DeploymentID: "dep-1", DatabaseID: "db-1", BindingID: "binding-1", CredentialGeneration: 3, TaskStatus: api.AppTaskStatusSucceeded, BindingIdentity: api.DataAPIInspectionIdentity{Status: "verified", Reason: "provider_target_and_trust_matched"}, Observation: raw})
				default:
					t.Errorf("unexpected API request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "owner-test-token")
			stdout, stderr, restore := swapIO(t)
			defer restore()
			code := run([]string{"--json", "data-api", "write-readiness", "--database", "db-1", "--app", "notes", "--binding", "binding-1", "--schema", "api", "--table", "notes", "--operations", "insert"})
			var report struct {
				Status          string `json:"status"`
				Reason          string `json:"reason"`
				BindingIdentity struct {
					Status string `json:"status"`
					Reason string `json:"reason"`
				} `json:"binding_identity"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("invalid report: %v output=%s", err, stdout.String())
			}
			wantReason, wantIdentity, wantIdentityReason, wantReads := "server_inspection_unsupported", "unknown", "server_inspection_unsupported", int64(1)
			switch scenario {
			case "binding_changed":
				wantReason, wantIdentityReason, wantReads = "binding_or_deployment_changed", "binding_or_deployment_changed", 2
			case "wrong_app":
				wantReason, wantIdentity, wantIdentityReason = "binding_selection_mismatch", "mismatch", "binding_belongs_to_different_app_or_database"
			case "missing_app":
				wantReason, wantIdentityReason = "binding_or_deployment_unavailable", "binding_or_deployment_unavailable"
			}
			if code == 0 || report.Status != "unavailable" || report.Reason != wantReason || report.BindingIdentity.Status != wantIdentity || report.BindingIdentity.Reason != wantIdentityReason {
				t.Fatalf("unexpected response: exit=%d report=%+v error=%s", code, report, stderr())
			}
			if bindingReads.Load() != wantReads {
				t.Fatalf("binding observed %d times, want %d", bindingReads.Load(), wantReads)
			}
		})
	}
}

func TestDataAPIWriteHostedUsesServingDeployment(t *testing.T) {
	f := newWriteHostedFixture()
	f.review.LatestDeploymentID = "newer-failed"
	scope, _ := dataAPIWriteScope("api", "notes", "insert")
	if _, err := inspectDataAPIWriteHosted(context.Background(), f, "notes", "db-1", "binding-1", scope); err != nil {
		t.Fatal(err)
	}
}
