package faas_test

import (
	"context"
	"encoding/json"
	preview "github.com/gregale-dev/gregale-go/preview"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnvironmentClientAndExtendedAdmissionContract(t *testing.T) {
	const id = "12345678-1234-4234-8234-123456789abc"
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("authentication lost")
		}
		if r.URL.Path == "/v2/executions" {
			var req preview.CreateExecutionRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.EnvironmentID != id || req.Limits.Class != preview.ExecutionClassExtended || req.Limits.TimeoutMS != 300000 {
				t.Errorf("admission contract: %+v", req)
			}
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "run-1", "status": "queued", "environment_id": id})
			return
		}
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "retiring"})
		case http.MethodGet:
			if r.URL.Path == "/v1/execution-environments" {
				_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": id, "status": "ready"}}})
				return
			}
			fallthrough
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "ready"})
		}
	}))
	defer srv.Close()
	c, err := preview.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	env, err := c.CreateExecutionEnvironment(ctx, preview.CreateExecutionEnvironmentRequest{Runtime: "node24", PackageJSON: "{}", PackageLock: "{}"})
	if err != nil || env.ID != id {
		t.Fatal(env, err)
	}
	if _, err = c.GetExecutionEnvironment(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rows, err := c.ListExecutionEnvironments(ctx); err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if _, err = c.CreateExecution(ctx, preview.CreateExecutionRequest{Runtime: "node24", Source: "export default ()=>42", EnvironmentID: id, Limits: &preview.ExecutionLimitRequest{Class: preview.ExecutionClassExtended, TimeoutMS: 300000}}); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteExecutionEnvironment(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 5 {
		t.Fatal(calls)
	}
}

func TestEnvironmentDiagnosticWatchCursorAndInterruption(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Query().Get("after") != "4" {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[{"sequence":8,"stage":"build","message":"installing","attempt":1,"created_at":"2026-11-15T00:00:00Z"}],"next_sequence":8,"gap":true,"status":"building"}`))
	}))
	defer srv.Close()
	c, err := preview.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	err = c.WatchExecutionEnvironment(ctx, "env", 4, func(page preview.ExecutionEnvironmentEventsResponse) error {
		if !page.Gap || page.NextSequence != 8 || len(page.Events) != 1 {
			t.Fatal(page)
		}
		cancel()
		return nil
	})
	if err != context.Canceled || calls != 1 {
		t.Fatal("watch cancellation changed build", err, calls)
	}
}
