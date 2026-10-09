// adr: 664
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJobOperationClientRejectsCredentialsAndRedirects(t *testing.T) {
	var redirects, calls int
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects++
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("wrong runtime authority")
		}
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	proof := OperationJobRuntimeProof{RunID: "run", InstanceID: "instance", Generation: 1, Attempt: 1, Capability: strings.Repeat("a", 64)}
	for _, capability := range []string{strings.Repeat("a", 64), ""} {
		proof.Capability = capability
		if _, err := NewClient(server.URL, "").GetJobOperationExecutionControl(t.Context(), "operation", proof); err == nil || redirects != 0 {
			t.Fatal("runtime proof followed redirect", err)
		}
	}
	if _, err := NewClient(server.URL, "account-token").GetJobOperationExecutionControl(t.Context(), "operation", proof); err == nil || calls != 2 {
		t.Fatal("account credentials reached runtime endpoint", err)
	}
}

func TestJobOperationDirectUploadClient(t *testing.T) {
	proof := OperationJobRuntimeProof{RunID: "run", InstanceID: "instance", Generation: 1, Attempt: 1, Capability: strings.Repeat("b", 64)}
	declaration := OperationArtifactUploadRequest{ReportID: "csv report", Name: "export +.csv", SizeBytes: 3, SHA256: "sha256:" + strings.Repeat("a", 64)}
	calls := 0
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "" || r.Header.Get(OperationJobCapabilityHeader) != proof.Capability {
			t.Error("wrong upload authority")
		}
		if strings.HasSuffix(r.URL.Path, "/artifact-uploads") {
			bytes, err := io.ReadAll(r.Body)
			if err != nil || string(bytes) != "csv" || r.Header.Get("Content-Type") != "application/octet-stream" || r.URL.Query().Get("report_id") != declaration.ReportID || r.URL.Query().Get("name") != declaration.Name || r.URL.Query().Get("sha256") != declaration.SHA256 {
				t.Error("upload bytes/metadata changed", err)
			}
		} else {
			var got OperationArtifactUploadRequest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got != declaration {
				t.Error("lookup declaration changed", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OperationJobArtifactResponse{Available: true, Artifact: &OperationResultArtifact{ID: "file"}})
	}))
	defer host.Close()
	cli := NewClient(host.URL, "")
	if _, err := cli.UploadJobOperationArtifact(t.Context(), "operation", proof, declaration, strings.NewReader("csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.ReuseJobOperationUpload(t.Context(), "operation", proof, declaration); err != nil {
		t.Fatal(err)
	}
	cli = NewClient(host.URL, "account-key")
	if _, err := cli.UploadJobOperationArtifact(t.Context(), "operation", proof, declaration, strings.NewReader("csv")); err == nil {
		t.Fatal("bearer accepted")
	}
	if _, err := cli.ReuseJobOperationUpload(t.Context(), "operation", proof, declaration); err == nil {
		t.Fatal("bearer accepted")
	}
	if calls != 2 {
		t.Fatal("credentialed requests reached upload API")
	}
	redirects := 0
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects++ }))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	cli = NewClient(redirect.URL, "")
	if _, err := cli.UploadJobOperationArtifact(t.Context(), "operation", proof, declaration, strings.NewReader("csv")); err == nil || redirects != 0 {
		t.Fatal("upload proof followed redirect", err)
	}
	if _, err := cli.ReuseJobOperationUpload(t.Context(), "operation", proof, declaration); err == nil || redirects != 0 {
		t.Fatal("lookup proof followed redirect", err)
	}
}

func TestJobOperationFileClientsUseTokenlessNativeProof(t *testing.T) {
	declaration := OperationArtifactRequest{ReportID: "csv", Name: "export.csv", URI: "obj://app/bucket/export.csv", SizeBytes: 3, SHA256: "sha256:" + strings.Repeat("a", 64)}
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Header.Get("X-Gregale-Operation-Job-Capability") != strings.Repeat("b", 64) {
			t.Error("wrong artifact authority")
		}
		var body OperationArtifactRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body != declaration {
			t.Error("declaration changed", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OperationJobArtifactResponse{Available: true, Artifact: &OperationResultArtifact{ID: "file", Name: body.Name, URI: body.URI, SizeBytes: body.SizeBytes, SHA256: body.SHA256}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "")
	proof := OperationJobRuntimeProof{RunID: "run", InstanceID: "instance", Generation: 1, Attempt: 1, Capability: strings.Repeat("b", 64)}
	prepared, err := client.PrepareJobOperationArtifact(t.Context(), "operation", proof, declaration)
	if err != nil || !prepared.Available || prepared.Artifact.ID != "file" {
		t.Fatal("prepare", err)
	}
	reused, err := client.ReuseJobOperationArtifact(t.Context(), "operation", proof, declaration)
	if err != nil || !reused.Available || reused.Artifact.ID != prepared.Artifact.ID {
		t.Fatal("replay", err)
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/artifacts") || !strings.HasSuffix(paths[1], "/artifact-receipts") {
		t.Fatal("wrong endpoints", paths)
	}
	client = NewClient(server.URL, "account-token")
	if _, err := client.PrepareJobOperationArtifact(t.Context(), "operation", proof, declaration); err == nil {
		t.Fatal("account credential accepted")
	}
	if _, err := client.ReuseJobOperationArtifact(t.Context(), "operation", proof, declaration); err == nil {
		t.Fatal("account credential accepted")
	}
	if len(paths) != 2 {
		t.Fatal("invalid credential reached server")
	}
}
