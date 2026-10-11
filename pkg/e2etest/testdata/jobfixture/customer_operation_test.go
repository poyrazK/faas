package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomerOperationFixture(t *testing.T) {
	for _, test := range []struct {
		name, mode         string
		generation         int
		cancelled, lostAck bool
		want               error
	}{
		{name: "successful private file", generation: 1},
		{name: "lost file acknowledgement", generation: 1, lostAck: true},
		{name: "direct private upload", mode: "direct-hold", generation: 1},
		{name: "lost direct upload acknowledgement", mode: "direct-hold", generation: 1, lostAck: true},
		{name: "cancelled before work", generation: 1, cancelled: true, want: errFixtureCancelled},
		{name: "unknown first generation", mode: "uncertain", generation: 1, want: errFixtureUncertain},
		{name: "approved second generation", mode: "uncertain", generation: 2},
		{name: "held result survives control outage", mode: "hold", generation: 1},
		{name: "cooperative cancellation after progress", mode: "cancel", generation: 1, want: errFixtureCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			var mu sync.Mutex
			controlCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/fixture/release" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get(api.OperationJobCapabilityHeader) != "private" || r.Header.Get(api.OperationJobRunHeader) != "run" || r.Header.Get(api.OperationJobInstanceHeader) != "instance" || r.Header.Get(api.OperationGenerationHeader) != strconv.Itoa(test.generation) || r.Header.Get(api.OperationAttemptHeader) != "1" {
					t.Error("fixture omitted native proof or sent a bearer")
				}
				calls = append(calls, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/control"):
					controlCalls++
					if test.mode == "hold" && controlCalls == 2 {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode(api.OperationJobControlResponse{CancellationRequested: test.cancelled || test.mode == "cancel" && controlCalls > 1})
				case strings.HasSuffix(r.URL.Path, "/artifacts"), strings.HasSuffix(r.URL.Path, "/artifact-uploads"):
					if strings.HasSuffix(r.URL.Path, "/artifact-uploads") {
						body, err := io.ReadAll(r.Body)
						if err != nil || string(body) != "csv" || r.URL.Query().Get("size_bytes") != "3" || r.URL.Query().Get("sha256") != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("csv"))) || r.Header.Get("Content-Type") != "application/octet-stream" {
							t.Error("direct upload bytes/declaration changed", err)
						}
					}
					if test.lostAck {
						w.WriteHeader(http.StatusBadGateway)
						_, _ = w.Write([]byte(`{"code":"lost_ack"}`))
						return
					}
					_ = json.NewEncoder(w).Encode(api.OperationJobArtifactResponse{Available: true, Artifact: &api.OperationResultArtifact{ID: "file"}})
				case strings.HasSuffix(r.URL.Path, "/artifact-receipts"), strings.HasSuffix(r.URL.Path, "/artifact-upload-receipts"):
					_ = json.NewEncoder(w).Encode(api.OperationJobArtifactResponse{Available: true, Artifact: &api.OperationResultArtifact{ID: "file"}})
				default:
					_ = json.NewEncoder(w).Encode(api.OperationResponse{ID: "operation"})
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := runCustomerOperation(ctx, api.NewClient(server.URL, ""), "operation", api.OperationJobRuntimeProof{RunID: "run", InstanceID: "instance", Generation: test.generation, Attempt: 1, Capability: "private"}, customerOperationInput{Mode: test.mode, Artifact: &api.OperationArtifactRequest{ReportID: "file", Name: "export.csv", SizeBytes: 3, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("csv")))}, ArtifactData: "csv"})
			if !errors.Is(err, test.want) {
				t.Fatalf("result=%v want %v", err, test.want)
			}
			mu.Lock()
			defer mu.Unlock()
			joined := strings.Join(calls, " ")
			if test.cancelled {
				if len(calls) != 1 {
					t.Fatalf("cancellation performed work: %v", calls)
				}
				return
			}
			if test.mode == "cancel" {
				if len(calls) != 3 {
					t.Fatalf("cancelled native fixture prepared a result: %v", calls)
				}
				return
			}
			if !strings.Contains(joined, "/progress") || !strings.Contains(joined, "/result") {
				t.Fatalf("missing progress or private result: %v", calls)
			}
			if (strings.Contains(joined, "/artifact-receipts") || strings.Contains(joined, "/artifact-upload-receipts")) != test.lostAck {
				t.Fatalf("acknowledgement recovery=%v", calls)
			}
		})
	}
}
