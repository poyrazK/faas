package main

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
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineCreateRegistersSourceAndProvisionsMirror(t *testing.T) {
	resetJSONOut(t)
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
			writeJSONTest(w, api.DataConnection{ID: sourceID, Name: req.Name, Revision: 1})
		case "POST /v1/postgres/databases":
			var req api.CreateManagedPostgresDatabaseRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Name != "orders-mirror" || req.Region != "test-region" || req.PostgresMajor != 16 {
				t.Error("mirror placement changed")
			}
			writeJSONTest(w, api.ManagedPostgresDatabase{ID: databaseID, State: "ready"})
		case "POST /v1/data/pipelines":
			var req api.CreateDataPipelineRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.SourceConnectionID != sourceID || req.DatabaseID != databaseID || len(req.ReadSubjects) != 1 || req.ReadSubjects[0] != "alice" {
				t.Error("pipeline did not bind the prepared resources")
			}
			writeJSONTest(w, api.DataPipeline{ID: pipelineID, State: "live", ObservationFresh: true, Revision: 1})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := cmdPipeline([]string{"create", "orders", "--url-file", file, "--region", "test-region", "--tables", "orders", "--read-subjects", "alice", "--wait"}); code != 0 {
		t.Fatalf("pipeline create exited %d", code)
	}
	if len(steps) != 3 {
		t.Fatalf("unexpected journey: %v", steps)
	}
	steps = nil
	if code := cmdPipeline([]string{"create", "invalid", "--url-file", file, "--region", "test-region", "--tables", "orders;drop", "--read-subjects", "alice"}); code == 0 {
		t.Fatal("invalid selection accepted")
	}
	if len(steps) != 0 {
		t.Fatal("invalid selection allocated resources")
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
	if _, err := waitDataPipeline(ctx, reader, p, "live", time.Millisecond); err != nil || reader.calls == 0 {
		t.Fatal("stale live status accepted or fresh status not observed")
	}
	p.ObservationFresh = true
	p.Verification = "inconclusive"
	if _, err := waitDataPipeline(ctx, reader, p, "verified", time.Millisecond); err == nil {
		t.Fatal("inconclusive verification passed")
	}
	p.State = "resync_required"
	p.Verification = "mismatch"
	p.Reason = "replica_mismatch"
	if _, err := waitDataPipeline(ctx, reader, p, "verified", time.Millisecond); err == nil {
		t.Fatal("mismatched replica passed")
	}
}

func TestPipelineWaitAcceptsDurableCleanupAfterHealthExpires(t *testing.T) {
	p := api.DataPipeline{ID: uuid.NewString(), State: "deleted", DesiredState: "deleted", Revision: 3, ObservedRevision: 3}
	reader := &pipelineStatusReader{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := waitDataPipeline(ctx, reader, p, "deleted", time.Millisecond); err != nil || reader.calls != 0 {
		t.Fatal("completed cleanup was treated as stale live health")
	}
	if _, err := waitDataPipeline(ctx, reader, p, "live", time.Millisecond); err == nil {
		t.Fatal("wait for an impossible live state did not fail")
	}
}
