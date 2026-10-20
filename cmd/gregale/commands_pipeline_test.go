package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineCreateRegistersSourceAndProvisionsMirror(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	sourceID, databaseID, pipelineID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	raw := "postgres://reader:private-source-password@source.example/orders?sslmode=verify-full"
	file := filepath.Join(t.TempDir(), "source-url")
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var steps []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		steps = append(steps, r.Method+" "+r.URL.Path)
		if r.Method == "POST" && r.Header.Get("Idempotency-Key") == "" {
			t.Error("resource creation missing recovery key")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/data/connections":
			var req api.CreateDataConnectionRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.ConnectionURL != raw || req.Name != "orders-source" {
				t.Error("source registration changed")
			}
			main.WriteJSONTest(w, api.DataConnection{ID: sourceID, Name: req.Name, Revision: 1})
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: sourceID, Name: "orders-source", Revision: 1}}})
		case "POST /v1/data/connections/" + sourceID + "/check":
			var req api.RequestDataConnectionCheck
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedRevision != 1 {
				t.Error("check request lost source revision")
			}
			main.WriteJSONTest(w, api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: sourceID, ConnectionRevision: 1, State: "passed", Fresh: true, Capacity: fixtureReadyPipelineCapacity(), Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}})
		case "POST /v1/postgres/databases":
			var req api.CreateManagedPostgresDatabaseRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Name != "orders-mirror" || req.Region != "test-region" || req.PostgresMajor != 16 {
				t.Error("mirror placement changed")
			}
			main.WriteJSONTest(w, api.ManagedPostgresDatabase{ID: databaseID, State: "ready"})
		case "POST /v1/data/pipelines":
			var req api.CreateDataPipelineRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.SourceConnectionID != sourceID || req.DatabaseID != databaseID || len(req.ReadSubjects) != 1 || req.ReadSubjects[0] != "alice" {
				t.Error("pipeline did not bind the prepared resources")
			}
			main.WriteJSONTest(w, api.DataPipeline{ID: pipelineID, State: "live", ObservationFresh: true, Revision: 1})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.CmdPipeline([]string{"create", "orders", "--url-file", file, "--region", "test-region", "--tables", "orders", "--read-subjects", "alice", "--wait"}); code != 0 {
		t.Fatalf("pipeline create exited %d", code)
	}
	if len(steps) != 7 {
		t.Fatalf("unexpected journey: %v", steps)
	}
	steps = nil
	if code := main.CmdPipeline([]string{"create", "invalid", "--url-file", file, "--region", "test-region", "--tables", "orders;drop", "--read-subjects", "alice"}); code == 0 {
		t.Fatal("invalid selection accepted")
	}
	if len(steps) != 0 {
		t.Fatal("invalid selection allocated resources")
	}
}

func TestPipelineCheckFailureStopsBeforeProvisioning(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	sourceID := uuid.NewString()
	provisioned := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: sourceID, Revision: 1}}})
		case "POST /v1/data/connections/" + sourceID + "/check":
			main.WriteJSONTest(w, api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: sourceID, State: "blocked", Fresh: true, Checks: []api.DataConnectionIssue{{Code: "source_replication_permission_required", State: "blocked", Remediation: "Grant REPLICATION"}}})
		default:
			provisioned = true
			http.Error(w, "unexpected provisioning", 500)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.CmdPipeline([]string{"create", "orders", "--connection", sourceID, "--tables", "orders", "--read-subjects", "alice"}); code == 0 {
		t.Fatal("blocked preflight succeeded")
	}
	if provisioned {
		t.Fatal("blocked preflight allocated a mirror or pipeline")
	}
}

func TestPipelineConnectionChecksAndTables(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	sourceID, databaseID := uuid.NewString(), uuid.NewString()
	check := api.DataConnectionCheck{ID: uuid.NewString(), ConnectionID: sourceID, ConnectionRevision: 2, DatabaseID: databaseID, State: "passed", Fresh: true, Capacity: fixtureReadyPipelineCapacity(), Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}}
	requests, jobs := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/connections":
			main.WriteJSONTest(w, api.DataConnectionList{Items: []api.DataConnection{{ID: sourceID, Revision: 2}}})
		case "POST /v1/data/connections/" + sourceID + "/check":
			var req api.RequestDataConnectionCheck
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedRevision != 2 || req.DatabaseID != databaseID {
				t.Error("connection check did not bind the selected credential and mirror")
			}
			jobs++
			main.WriteJSONTest(w, check)
		case "GET /v1/data/connections/" + sourceID + "/check":
			main.WriteJSONTest(w, check)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.CmdPipeline([]string{"connection", "check", sourceID, "--database", databaseID}); code != 0 {
		t.Fatalf("connection check exited %d", code)
	}
	if code := main.CmdPipeline([]string{"connection", "tables", sourceID}); code != 0 {
		t.Fatalf("table inspection exited %d", code)
	}
	if jobs != 1 || requests != 3 {
		t.Fatal("table inspection unexpectedly probed the source again")
	}
	if code := main.CmdPipeline([]string{"connection", "tables", sourceID, "--database", databaseID}); code == 0 || requests != 3 {
		t.Fatal("table inspection silently accepted a mirror it did not check")
	}
}

type pipelineStatusReader struct {
	next  api.DataPipeline
	calls int
}

func (r *pipelineStatusReader) GetDataPipeline(context.Context, string) (api.DataPipeline, error) {
	r.calls++
	return r.next, nil
}
func TestPipelineWaitRequiresFreshHealthAndConclusiveVerification(t *testing.T) {
	p := api.DataPipeline{ID: uuid.NewString(), State: "live", Verification: "passed"}
	reader := &pipelineStatusReader{next: p}
	reader.next.ObservationFresh = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := main.WaitDataPipeline(ctx, reader, p, "live", time.Millisecond); err != nil || reader.calls == 0 {
		t.Fatal("stale live status accepted or fresh status not observed")
	}
	p.ObservationFresh = true
	p.Verification = "inconclusive"
	if _, err := main.WaitDataPipeline(ctx, reader, p, "verified", time.Millisecond); err == nil {
		t.Fatal("inconclusive verification passed")
	}
	p.State = "resync_required"
	p.Verification = "mismatch"
	p.Reason = "replica_mismatch"
	if _, err := main.WaitDataPipeline(ctx, reader, p, "verified", time.Millisecond); err == nil {
		t.Fatal("mismatched replica passed")
	}
}

func TestPipelineWaitAcceptsDurableCleanupAfterHealthExpires(t *testing.T) {
	p := api.DataPipeline{ID: uuid.NewString(), State: "deleted", DesiredState: "deleted", Revision: 3, ObservedRevision: 3}
	reader := &pipelineStatusReader{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := main.WaitDataPipeline(ctx, reader, p, "deleted", time.Millisecond); err != nil || reader.calls != 0 {
		t.Fatal("completed cleanup was treated as stale live health")
	}
	if _, err := main.WaitDataPipeline(ctx, reader, p, "live", time.Millisecond); err == nil {
		t.Fatal("wait for an impossible live state did not fail")
	}
}

func fixtureReadyPipelineCapacity() *api.DataPipelineCapacity {
	now := time.Now().UTC()
	endpoint := api.DataEndpointCapacity{State: "ready", RequiredConnections: 5, UsableConnections: 100, ObservedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(api.DataPipelineCapacityMaxAge).Format(time.RFC3339Nano)}
	return &api.DataPipelineCapacity{State: "ready", Source: &endpoint, Destination: &endpoint}
}

func TestPipelineCapacityRejectsUnknownExpiredOrIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"unknown", "expired", "incomplete", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			c := api.DataConnectionCheck{Capacity: fixtureReadyPipelineCapacity()}
			switch mode {
			case "unknown":
				c.Capacity.Source.State = "needs_qualification"
			case "expired":
				c.Capacity.Source.ExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
			case "incomplete":
				c.Capacity.Source.ObservedAt = ""
			case "exhausted":
				c.Capacity.Source.ReservedConnections = 100
			}
			if main.CheckCapacityForCreate(c, true) == nil {
				t.Fatal("invalid capacity permitted provisioning")
			}
		})
	}
}
