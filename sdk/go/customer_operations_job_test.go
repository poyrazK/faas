// adr: 664
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestJobOperationRuntimeUsesTaskProofWithoutAccountCredentials(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Gregale-Operation-Job-Capability") != strings.Repeat("a", 64) || r.Header.Get("X-Gregale-Operation-Job-Instance-Id") != "instance" {
			t.Error("wrong authority")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "control") {
			_, _ = w.Write([]byte(`{"operation_id":"operation","job_run_id":"run","generation":1,"attempt":1,"cancellation_requested":false}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"operation","state":"running"}`))
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	proof := faas.OperationJobRuntimeProof{RunID: "run", InstanceID: "instance", Generation: 1, Attempt: 1, Capability: strings.Repeat("a", 64)}
	if _, err := client.GetJobOperationExecutionControl(context.Background(), "operation", proof); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PrepareJobOperationResult(context.Background(), "operation", proof, faas.OperationJobReportRequest{ReportID: "result", Result: json.RawMessage(`{"file":"export.csv"}`)}); err != nil {
		t.Fatal(err)
	}
	client.SetToken("account-token")
	if _, err := client.GetJobOperationExecutionControl(context.Background(), "operation", proof); err == nil {
		t.Fatal("account client used as task runtime")
	}
	if calls != 2 {
		t.Fatal("authorization failure performed networking")
	}
	bytes, err := json.Marshal(proof)
	if err != nil || strings.Contains(string(bytes), proof.Capability) {
		t.Fatal("proof leaked under JSON formatting")
	}
}
func TestJobOperationRuntimeDoesNotFollowRedirects(t *testing.T) {
	var leaked int
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked++; w.WriteHeader(200) }))
	defer sink.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetJobOperationExecutionControl(context.Background(), "operation", faas.OperationJobRuntimeProof{RunID: "run", InstanceID: "instance", Generation: 1, Attempt: 1, Capability: strings.Repeat("a", 64)})
	if err == nil || leaked != 0 {
		t.Fatal("runtime capability followed redirect")
	}
}

func TestJobOperationFileClientsUseTokenlessNativeProof(t *testing.T) {
	declaration := faas.OperationArtifactRequest{ReportID: "csv", Name: "export.csv", URI: "obj://app/bucket/export.csv", SizeBytes: 3, SHA256: "sha256:" + strings.Repeat("a", 64)}
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Header.Get("X-Gregale-Operation-Job-Capability") != strings.Repeat("b", 64) {
			t.Error("wrong artifact authority")
		}
		var body faas.OperationArtifactRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body != declaration {
			t.Error("declaration changed", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(faas.OperationJobArtifactResponse{Available: true, Artifact: &faas.OperationResultArtifact{ID: "file", Name: body.Name, URI: body.URI, SizeBytes: body.SizeBytes, SHA256: body.SHA256}})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	proof := faas.OperationJobRuntimeProof{RunID: "run", InstanceID: "instance", Generation: 1, Attempt: 1, Capability: strings.Repeat("b", 64)}
	prepared, err := client.PrepareJobOperationArtifact(context.Background(), "operation", proof, declaration)
	if err != nil || !prepared.Available || prepared.Artifact.ID != "file" {
		t.Fatal("prepare", err)
	}
	reused, err := client.ReuseJobOperationArtifact(context.Background(), "operation", proof, declaration)
	if err != nil || !reused.Available || reused.Artifact.ID != prepared.Artifact.ID {
		t.Fatal("replay", err)
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/artifacts") || !strings.HasSuffix(paths[1], "/artifact-receipts") {
		t.Fatal("wrong endpoints", paths)
	}
	client.SetToken("account-token")
	if _, err := client.PrepareJobOperationArtifact(context.Background(), "operation", proof, declaration); err == nil {
		t.Fatal("account credential accepted")
	}
	if _, err := client.ReuseJobOperationArtifact(context.Background(), "operation", proof, declaration); err == nil {
		t.Fatal("account credential accepted")
	}
	if len(paths) != 2 {
		t.Fatal("invalid credential reached server")
	}
}
