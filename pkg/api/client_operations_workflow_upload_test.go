// adr: 608
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkflowUploadClientUsesWorkloadProofAndRejectsRedirects(t *testing.T) {
	proof := OperationWorkflowRuntimeProof{RunID: "run", StepName: "finish", Generation: 2, Attempt: 2, Capability: "33333333-3333-4333-8333-333333333333"}
	declaration := OperationArtifactUploadRequest{ReportID: "csv", Name: "export.csv", SizeBytes: 3, SHA256: "sha256:" + strings.Repeat("a", 64)}
	calls := 0
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer workload" || r.Header.Get(OperationWorkflowRunHeader) != proof.RunID || r.Header.Get(OperationWorkflowStepHeader) != proof.StepName || r.Header.Get(OperationGenerationHeader) != "2" || r.Header.Get(OperationExecutionKindHeader) != "workflow" || r.Header.Get(InvocationIDHeader) != "" || r.Header.Get(OperationAttemptHeader) != "2" || r.Header.Get(OperationWorkflowCapabilityHeader) != proof.Capability {
			t.Error("wrong HTTP proof")
		}
		if strings.HasSuffix(r.URL.Path, "/artifact-uploads") {
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "csv" || r.Header.Get("Content-Type") != "application/octet-stream" || r.URL.Query().Get("sha256") != declaration.SHA256 {
				t.Error("changed upload bytes", err)
			}
		} else {
			var got OperationArtifactUploadRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got != declaration {
				t.Error("changed declaration", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OperationWorkflowArtifactResponse{Available: true, Artifact: &OperationResultArtifact{ID: "file"}})
	}))
	defer host.Close()
	client := NewClient(host.URL, "workload")
	if _, err := client.UploadWorkflowOperationArtifact(t.Context(), "operation", proof, declaration, strings.NewReader("csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReuseWorkflowOperationUpload(t.Context(), "operation", proof, declaration); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("wrong request count")
	}
	redirects := 0
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects++ }))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client = NewClient(redirect.URL, "workload")
	if _, err := client.UploadWorkflowOperationArtifact(t.Context(), "operation", proof, declaration, strings.NewReader("csv")); err == nil || redirects != 0 {
		t.Fatal("upload followed redirect", err)
	}
	if _, err := client.ReuseWorkflowOperationUpload(t.Context(), "operation", proof, declaration); err == nil || redirects != 0 {
		t.Fatal("lookup followed redirect", err)
	}
}
