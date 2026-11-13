// adr: 20261112220000 — CLI pipeline recovery, preflight, naming and confirmation.
package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type recoveredPipeline struct{ calls int }

func (r *recoveredPipeline) GetDataPipeline(context.Context, string) (api.DataPipeline, error) {
	r.calls++
	return api.DataPipeline{State: "live", Revision: 2, ObservedRevision: 2, ObservationFresh: true}, nil
}
func TestPipelineWaitPollsPastOldFailure(t *testing.T) {
	reader := &recoveredPipeline{}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	p, err := main.WaitDataPipeline(ctx, reader, api.DataPipeline{ID: uuid.NewString(), State: "blocked", Reason: "source_authentication_failed", Revision: 2, ObservedRevision: 1}, "live", time.Millisecond)
	if err != nil || reader.calls != 1 || p.State != "live" {
		t.Fatal("stale failure aborted recovery", p, reader.calls, err)
	}
}

func TestPipelineResyncRequiresExplicitAutomationConfirmation(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test")
	id := uuid.NewString()
	mutations, reads := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			reads++
			main.WriteJSONTest(w, api.DataPipeline{ID: id, Revision: 1, State: "live"})
			return
		}
		mutations++
		main.WriteJSONTest(w, api.DataPipeline{ID: id, Revision: 2, State: "validating"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.Run([]string{"--non-interactive", "--json", "pipeline", "resync", id}); code == 0 || mutations != 0 || reads != 0 {
		t.Fatal("unconfirmed automation touched resources", code, mutations, reads)
	}
	if code := main.Run([]string{"--non-interactive", "--json", "pipeline", "resync", id, "--yes"}); code != 0 || mutations != 1 {
		t.Fatal("confirmed automation failed", code, mutations)
	}
}

func TestPipelineGeneratedMirrorNamesAcceptDigitLedSlugs(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test")
	source, database := uuid.NewString(), uuid.NewString()
	generated := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/pipelines":
			main.WriteJSONTest(w, api.DataPipelineList{Items: []api.DataPipeline{}})
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: source, Revision: 1}}})
		case "POST /v1/data/connections/" + source + "/check":
			main.WriteJSONTest(w, api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: source, State: "passed", Fresh: true, Capacity: fixtureReadyPipelineCapacity(), Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}})
		case "POST /v1/postgres/databases":
			var request api.CreateManagedPostgresDatabaseRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			generated = request.Name
			main.WriteJSONTest(w, api.ManagedPostgresDatabase{ID: database, State: "ready"})
		case "POST /v1/data/pipelines":
			main.WriteJSONTest(w, api.DataPipeline{ID: uuid.NewString(), State: "validating"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.CmdPipeline([]string{"create", "123-orders", "--connection", source, "--region", "test-region", "--tables", "orders", "--read-subjects", "alice"}); code != 0 || !managedpostgres.ValidName(generated) || generated != "p-123-orders-mirror-db" {
		t.Fatal("valid pipeline cannot provision", code, generated)
	}
}

func TestPipelineLongSourceNamesAreDistinctAndRetryStable(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test")
	source := uuid.NewString()
	var names, keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/pipelines":
			main.WriteJSONTest(w, api.DataPipelineList{Items: []api.DataPipeline{}})
		case "POST /v1/data/connections":
			var request api.CreateDataConnectionRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			names = append(names, request.Name)
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			main.WriteJSONTest(w, api.DataConnection{ID: source, Revision: 1})
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: source, Revision: 1}}})
		case "POST /v1/data/connections/" + source + "/check":
			main.WriteJSONTest(w, api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: source, State: "blocked", Fresh: true})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	for _, suffix := range []string{"north", "south", "north"} {
		file := filepath.Join(t.TempDir(), "url")
		if err := os.WriteFile(file, []byte("postgres://r:local-test@"+suffix+".example/db?sslmode=verify-full"), 0600); err != nil {
			t.Fatal(err)
		}
		_ = main.CmdPipeline([]string{"create", strings.Repeat("a", 32) + "-" + suffix, "--url-file", file, "--region", "test-region", "--tables", "orders", "--read-subjects", "alice"})
	}
	if len(names) != 3 || names[0] == names[1] || names[0] != names[2] || keys[0] == "" || keys[0] != keys[2] {
		t.Fatal("generated source identity collided or changed on retry", names, keys)
	}
	for _, name := range names {
		if !api.ValidAppSlug(name) {
			t.Fatal("invalid generated source", name)
		}
	}
}

func TestPipelineAuthPreflightPrecedesEveryMutation(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test")
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	mutations, lookups := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
			t.Error("provisioned before Auth validation")
		}
		if r.URL.Path == "/v1/apps/missing-auth" {
			lookups++
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	file := filepath.Join(t.TempDir(), "url")
	if err := os.WriteFile(file, []byte("postgres://r:local-test@source.example/db?sslmode=verify-full"), 0600); err != nil {
		t.Fatal(err)
	}
	code := main.CmdPipeline([]string{"create", "orders", "--url-file", file, "--region", "test-region", "--tables", "orders", "--read-subjects", "alice", "--api", "orders-api", "--auth", "missing-auth"})
	if code == 0 || mutations != 0 || lookups != 1 {
		t.Fatal("missing Auth was not preflighted", code, mutations, lookups)
	}
	code = main.CmdPipeline([]string{"create", "orders", "--url-file", file, "--region", "test-region", "--tables", "orders", "--read-subjects", "alice", "--api", "api", "--auth", "missing-auth"})
	if code == 0 || mutations != 0 || lookups != 1 {
		t.Fatal("reserved API name was not rejected before provisioning", code, mutations, lookups)
	}
}

func TestPipelineConnectionLookupCanonicalizesUUIDs(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test")
	id := "aabbccdd-1122-4333-8444-5566778899aa"
	checks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: id, Revision: 1}}})
		case "POST /v1/data/connections/" + id + "/check":
			checks++
			main.WriteJSONTest(w, api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: id, State: "passed", Fresh: true})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	for _, value := range []string{strings.ToUpper(id), strings.ReplaceAll(id, "-", ""), "{" + id + "}", "urn:uuid:" + id} {
		if code := main.CmdPipeline([]string{"connection", "tables", value, "--tables", "orders", "--wait=false"}); code != 0 {
			t.Fatal("valid spelling rejected", value, code)
		}
	}
	if checks != 4 {
		t.Fatal("canonical source check missing", checks)
	}
}
