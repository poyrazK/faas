package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

func nativeReplicaReceipt(instance string, budget any) string {
	receipt := nativeDrainReceipt(instance, budget)
	if instance == "two" {
		return strings.ReplaceAll(receipt, "12345678-1234-1234-1234-123456789abc", "22222222-2222-4222-8222-222222222222")
	}
	return receipt
}

func TestMCPNativeWorkerReceiptsRecoverAfterActualLogEviction(t *testing.T) {
	ring := logbuf.New(0)
	startup := `{"event":"mcp_task_worker_started","workerID":"12345678-1234-1234-1234-123456789abc","claimFence":true,"shutdownTimeoutMs":10000}`
	if _, err := ring.Write("stdout", []byte(startup+"\n")); err != nil {
		t.Fatal(err)
	}
	var follows atomic.Int32
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/previous/instances":
			_ = json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "instance", DeploymentID: "old", State: "running"}})
		case "/v1/apps/previous/logs":
			if r.URL.Query().Get("follow") == "1" {
				follows.Add(1)
				if r.URL.Query().Get("since") != "30s" || r.URL.Query().Get("grep") != "mcp_task_worker_started" {
					t.Errorf("live window missing: %s", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, nativeReplicaReceipt("instance", 10000))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
				return
			}
			for _, line := range ring.Snapshot(1) {
				if strings.Contains(line.Line, "mcp_task_worker_started") {
					fmt.Fprint(w, nativeReplicaReceipt("instance", 10000))
				}
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	app := api.AppResponse{ID: "previous", Manifest: api.AppManifest{StopGracePeriod: 45 * time.Second}}
	client := main.NewClient(server.URL, "operator")
	for _, evicted := range []bool{false, true} {
		if evicted {
			noise := []byte(strings.Repeat("x", 1024) + "\n")
			for i := 0; i <= logbuf.DefaultMaxBytes/1024; i++ {
				if _, err := ring.Write("stdout", noise); err != nil {
					t.Fatal(err)
				}
			}
			if ring.LowestRetainedSeq() <= 1 {
				t.Fatal("startup did not leave the real default-size ring")
			}
		}
		ids, err := main.McpNativeDrainableWorkerIDs(t.Context(), client, app, "previous", "old", false)
		if err != nil || len(ids) != 1 || ids[0] != "12345678-1234-1234-1234-123456789abc" {
			t.Fatalf("evicted=%v ids=%v err=%v", evicted, ids, err)
		}
	}
	if follows.Load() != 1 {
		t.Fatalf("follow requests=%d", follows.Load())
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("complete receipts did not close the live log stream")
	}
}

func TestMCPNativeFreshReceiptsRequireCompleteValidCurrentReplicas(t *testing.T) {
	for _, tc := range []struct {
		name, retained, fresh string
		wantOK, wantFollow    bool
	}{
		{"fresh complete", "", nativeReplicaReceipt("one", 10000) + nativeReplicaReceipt("two", 20000), true, true},
		{"partial retained", nativeReplicaReceipt("one", 10000), nativeReplicaReceipt("one", 10000) + nativeReplicaReceipt("two", 20000), true, true},
		{"missing replica", "", nativeReplicaReceipt("one", 10000), false, true},
		{"unrelated instance", "", nativeReplicaReceipt("other", 10000), false, true},
		{"invalid retained plus missing replica", nativeReplicaReceipt("one", -1), "", false, false},
		{"fractional retained budget cannot use fallback", nativeReplicaReceipt("one", 1000.5), nativeReplicaReceipt("one", 10000) + nativeReplicaReceipt("two", 10000), false, false},
		{"string retained budget cannot use fallback", nativeReplicaReceipt("one", "10000"), nativeReplicaReceipt("one", 10000) + nativeReplicaReceipt("two", 10000), false, false},
		{"invalid live plus missing replica", "", nativeReplicaReceipt("one", -1), false, true},
		{"ambiguous live identity", "", nativeReplicaReceipt("one", 10000) + strings.ReplaceAll(nativeReplicaReceipt("one", 10000), "12345678-1234-1234-1234-123456789abc", "11111111-1111-4111-8111-111111111111"), false, true},
		{"longer than stop grace", "", nativeReplicaReceipt("one", 120000) + nativeReplicaReceipt("two", 10000), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var followed atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/instances") {
					_ = json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "one", DeploymentID: "current", State: "running"}, {ID: "two", DeploymentID: "current", State: "running"}})
					return
				}
				if r.URL.Query().Get("follow") == "1" {
					followed.Store(true)
					fmt.Fprint(w, tc.fresh)
				} else {
					fmt.Fprint(w, tc.retained)
				}
			}))
			defer server.Close()
			app := api.AppResponse{Manifest: api.AppManifest{StopGracePeriod: 45 * time.Second}}
			_, err := main.McpNativeDrainableWorkerIDs(t.Context(), main.NewClient(server.URL, "operator"), app, "worker", "current", true)
			if (err == nil) != tc.wantOK || followed.Load() != tc.wantFollow {
				t.Fatalf("err=%v followed=%v", err, followed.Load())
			}
		})
	}
}

func TestMCPNativeFreshReceiptWaitHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/instances") {
			_ = json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "one", DeploymentID: "current", State: "running"}})
			return
		}
		if r.URL.Query().Get("follow") == "1" {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := main.McpNativeWorkerIDs(ctx, main.NewClient(server.URL, "operator"), "worker", "current", true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("receipt wait did not honor cancellation: %v", err)
	}
}
