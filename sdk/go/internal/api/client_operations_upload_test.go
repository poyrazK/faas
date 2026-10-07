// adr: 669
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPUploadClientUsesWorkloadProofAndRejectsRedirects(t *testing.T) {
	proof := OperationRuntimeProof{InvocationID: "invocation", Attempt: 2, Capability: strings.Repeat("b", 64)}
	declaration := OperationArtifactUploadRequest{ReportID: "csv", Name: "export.csv", SizeBytes: 3, SHA256: "sha256:" + strings.Repeat("a", 64)}
	calls := 0
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer workload" || r.Header.Get(InvocationIDHeader) != proof.InvocationID || r.Header.Get(OperationAttemptHeader) != "2" || r.Header.Get(OperationCapabilityHeader) != proof.Capability {
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
		_ = json.NewEncoder(w).Encode(OperationArtifactUploadResponse{Available: true, Artifact: &OperationResultArtifact{ID: "file"}})
	}))
	defer host.Close()
	client := NewClient(host.URL, "workload")
	if _, err := client.UploadOperationArtifact(context.Background(), "operation", proof, declaration, strings.NewReader("csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReuseOperationUpload(context.Background(), "operation", proof, declaration); err != nil {
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
	if _, err := client.UploadOperationArtifact(context.Background(), "operation", proof, declaration, strings.NewReader("csv")); err == nil || redirects != 0 {
		t.Fatal("upload followed redirect", err)
	}
	if _, err := client.ReuseOperationUpload(context.Background(), "operation", proof, declaration); err == nil || redirects != 0 {
		t.Fatal("lookup followed redirect", err)
	}
}
