package faas_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	preview "github.com/gregale-dev/gregale-go/preview"
)

func TestExecutionFilesStreamingAndV2ContractReceipt(t *testing.T) {
	const id = "12345678-1234-4234-8234-123456789abc"
	data := bytes.Repeat([]byte("file"), 300000)
	hash := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("file request lost authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/executions" {
			if r.Header.Get("Idempotency-Key") != "same-key" {
				t.Error("admission key changed")
			}
			var req preview.CreateExecutionRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.ResultSchema) == 0 || len(req.InputFiles) != 1 {
				t.Error("contract or file reference lost")
			}
			_, _ = io.WriteString(w, `{"id":"run-1","status":"succeeded","result":42,"result_validation":{"status":"invalid","message":"Run result does not match result_schema"}}`)
			return
		}
		if r.URL.Path == "/v1/executions" {
			t.Error("extended request used v1")
		}
		if r.Method == "PUT" {
			got, _ := io.ReadAll(r.Body)
			if !bytes.Equal(got, data) || r.ContentLength != int64(len(data)) || r.Header.Get("X-Content-SHA256") != digest {
				t.Error("incorrect upload bytes or integrity metadata")
			}
		}
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)
			return
		}
		_ = json.NewEncoder(w).Encode(preview.ExecutionFileResponse{ID: id, Status: "ready", SizeBytes: int64(len(data)), SHA256: digest, ContentURL: "/v1/execution-files/" + id + "/content"})
	}))
	defer srv.Close()
	client, err := preview.NewClient(srv.URL, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	meta, err := client.PrepareExecutionFile(ctx, preview.CreateExecutionFileRequest{SizeBytes: int64(len(data)), SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.UploadExecutionFile(ctx, meta.ID, bytes.NewReader(data), meta.SizeBytes, meta.SHA256); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err = client.DownloadExecutionFile(ctx, id, &output); err != nil || !bytes.Equal(output.Bytes(), data) {
		t.Fatal("download", err)
	}
	receipt, err := client.CreateExecutionWithIdempotencyKey(ctx, preview.CreateExecutionRequest{Runtime: "node24", Source: "return 42", ResultSchema: json.RawMessage(`{"type":"string"}`), InputFiles: []preview.ExecutionFileInput{{FileID: id, Path: "data.bin"}}}, "same-key")
	if err != nil || !receipt.ContractFailed() || receipt.Status != "succeeded" {
		t.Fatal("contract receipt", err)
	}
	if _, err = (preview.ExecutionArtifact{FileID: id}).Bytes(); err == nil {
		t.Fatal("stored artifact decoded as inline bytes")
	}
}

func TestExecutionFilesEmptyUploadHasFixedLength(t *testing.T) {
	const id = "12345678-1234-4234-8234-123456789abc"
	hash := sha256.Sum256(nil)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			t.Error("empty upload must not use unknown-length chunked transfer")
		}
		_ = json.NewEncoder(w).Encode(preview.ExecutionFileResponse{ID: id, Status: "ready", SHA256: digest})
	}))
	defer srv.Close()
	client, err := preview.NewClient(srv.URL, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.UploadExecutionFile(context.Background(), id, bytes.NewReader(nil), 0, digest); err != nil {
		t.Fatal(err)
	}
}
