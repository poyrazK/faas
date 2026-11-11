package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestPipelineCreateRecoversAcceptedIntentBeforeProbingMirror(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test-token")
	source, database, pipeline := uuid.NewString(), uuid.NewString(), uuid.NewString()
	existing := api.DataPipeline{ID: pipeline, Name: "orders", SourceConnectionID: source, DatabaseID: database, Tables: []string{"customers", "orders"}, ReadSubjects: []string{"alice", "bob"}, State: "live", DesiredState: "running", Revision: 1, ObservedRevision: 1, ObservationFresh: true}
	committed, recovering := false, false
	creates, checks, mirrors, inspections := 0, 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: source, Revision: 1}}})
		case "GET /v1/data/pipelines":
			items := []api.DataPipeline{}
			if committed {
				items = append(items, existing)
			}
			main.WriteJSONTest(w, api.DataPipelineList{Items: items})
		case "GET /v1/data/pipelines/" + pipeline:
			inspections++
			main.WriteJSONTest(w, existing)
		case "POST /v1/data/connections/" + source + "/check":
			checks++
			var req api.RequestDataConnectionCheck
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			check := api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: source, DatabaseID: req.DatabaseID, State: "passed", Fresh: true, Capacity: fixtureReadyPipelineCapacity(), Tables: []api.DataSourceTable{{Name: "orders", Supported: true}, {Name: "customers", Supported: true}}}
			if committed {
				check.State = "blocked"
				check.Checks = []api.DataConnectionIssue{{Code: "destination_not_empty", State: "blocked"}}
			}
			main.WriteJSONTest(w, check)
		case "POST /v1/postgres/databases":
			mirrors++
			main.WriteJSONTest(w, api.ManagedPostgresDatabase{ID: database, Name: "orders-mirror", Region: "test-region", State: "ready"})
		case "GET /v1/postgres/databases":
			main.WriteJSONTest(w, api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: database, Name: "orders-mirror", Region: "test-region", State: "ready"}}})
		case "GET /v1/postgres/databases/" + database:
			main.WriteJSONTest(w, api.ManagedPostgresDatabase{ID: database, Region: "test-region", State: "ready"})
		case "POST /v1/data/pipelines":
			creates++
			committed = true
			if !recovering {
				api.WriteProblem(w, api.NewProblem(503, "data_pipeline_unavailable", "Reply lost", "Inspect retained resources before retrying."))
				return
			}
			main.WriteJSONTest(w, existing)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.CmdPipeline([]string{"create", "orders", "--connection", source, "--region", "test-region", "--tables", "orders,customers", "--read-subjects", "bob,alice"}); code == 0 || !committed {
		t.Fatal("fixture did not lose an accepted reply", code, committed)
	}
	recovering = true
	oldCreates, oldChecks := creates, checks
	for _, flags := range [][]string{{"--database", strings.ToUpper(database)}, {"--database", "orders-mirror"}, {"--region", "test-region"}, nil} {
		args := []string{"create", "orders", "--connection", strings.ToUpper(source), "--tables", "customers,orders", "--read-subjects", "alice,bob", "--wait"}
		if code := main.CmdPipeline(append(args, flags...)); code != 0 {
			t.Fatalf("accepted intent did not recover: flags=%v exit=%d", flags, code)
		}
	}
	if checks != oldChecks || creates != oldCreates || mirrors != 1 || inspections != 4 {
		t.Fatal("recovery probed, reprovisioned or failed to read current state", checks, creates, mirrors, inspections)
	}
	for _, changed := range []string{"source", "tables", "readers", "database", "region", "deleted"} {
		args := []string{"create", "orders", "--connection", source, "--database", database, "--tables", "orders,customers", "--read-subjects", "alice,bob"}
		saved := existing
		switch changed {
		case "source":
			existing.SourceConnectionID = uuid.NewString()
		case "tables":
			existing.Tables = []string{"orders"}
		case "readers":
			existing.ReadSubjects = []string{"carol"}
		case "database":
			args[5] = uuid.NewString()
		case "region":
			args = append(args, "--region", "other-region")
		case "deleted":
			existing.DesiredState = "deleted"
		}
		if code := main.CmdPipeline(args); code == 0 {
			t.Fatalf("different %s intent was adopted", changed)
		}
		existing = saved
	}
	if checks != oldChecks || creates != oldCreates || mirrors != 1 {
		t.Fatal("conflicting recovery allocated resources")
	}
}

func TestPipelineCreateGeneratesValidStableMirrorNames(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test-token")
	source, database := uuid.NewString(), uuid.NewString()
	name, generated := "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: source, Revision: 1}}})
		case "GET /v1/data/pipelines":
			main.WriteJSONTest(w, api.DataPipelineList{Items: []api.DataPipeline{}})
		case "POST /v1/data/connections/" + source + "/check":
			main.WriteJSONTest(w, api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: source, State: "passed", Fresh: true, Capacity: fixtureReadyPipelineCapacity(), Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}})
		case "POST /v1/postgres/databases":
			var req api.CreateManagedPostgresDatabaseRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			generated = req.Name
			if !managedpostgres.ValidName(generated) {
				t.Errorf("invalid derived mirror name %q for %q", generated, name)
			}
			main.WriteJSONTest(w, api.ManagedPostgresDatabase{ID: database, Name: generated, State: "ready"})
		case "POST /v1/data/pipelines":
			main.WriteJSONTest(w, api.DataPipeline{ID: uuid.NewString(), DatabaseID: database, Name: name, State: "validating"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	names := []string{"orders", "7-orders", "p-7-orders", "7-orders-mirror", "7" + strings.Repeat("a", api.AppSlugMaxLength-2) + "b", "7" + strings.Repeat("a", api.AppSlugMaxLength-2) + "c", strings.Repeat("a", api.AppSlugMaxLength)}
	seen := map[string]string{}
	for _, slug := range names {
		name = slug
		for range 2 {
			if code := main.CmdPipeline([]string{"create", name, "--connection", source, "--region", "test-region", "--tables", "orders", "--read-subjects", "alice"}); code != 0 {
				t.Fatalf("valid slug %q rejected: %d", name, code)
			}
			if previous := seen[name]; previous != "" && previous != generated {
				t.Fatal("mirror name changed on retry", name, previous, generated)
			}
			for other, mirror := range seen {
				if other != name && mirror == generated {
					t.Fatal("different slugs lost their distinction", other, name, generated)
				}
			}
			seen[name] = generated
		}
	}
	if seen["orders"] != "orders-mirror" || seen["7-orders"] != "p-7-orders-mirror-db" {
		t.Fatal("unexpected short-name derivation", seen)
	}
}
