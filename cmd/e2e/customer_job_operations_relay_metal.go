//go:build metal

package e2e

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// Test transport only: it neither mints proofs nor completes tasks. Requests
// are validated against real native leases by the unmodified apid runtime.
type customerJobRelay struct {
	server                     *http.Server
	socket                     string
	release, lostAck, prepared atomic.Bool
	receiptRequests            atomic.Int32
	mu                         sync.Mutex
	observed                   api.OperationJobRuntimeProof
}

func (f *customerJobFixture) connect(t *testing.T, id string, loseArtifactAck bool) (*customerJobRelay, string) {
	t.Helper()
	run, instance, root := "", "", ""
	customerJobEventually(t, 2*time.Minute, "native guest jail", func() bool {
		operation, err := f.store.OperationByID(context.Background(), f.account.ID, f.tenant.ID, id)
		if err != nil || operation.JobRunID == "" {
			return false
		}
		run = operation.JobRunID
		task, err := f.store.JobTaskGet(context.Background(), run, 0)
		if err != nil || task.InstanceID == nil {
			return false
		}
		instance = *task.InstanceID
		sockets, err := filepath.Glob(filepath.Join(fcvm.JailChrootBase, "firecracker*", instance, "root", fcvm.VsockUDSSocketName))
		if err != nil || len(sockets) != 1 {
			return false
		}
		root = filepath.Dir(sockets[0])
		info, err := os.Stat(sockets[0])
		return err == nil && info.Mode()&os.ModeSocket != 0
	})
	socket := filepath.Join(root, fcvm.VsockUDSSocketName+"_19041")
	listener, err := net.Listen("unix", socket)
	mustCustomerJob(t, err)
	// Firecracker runs under the jail's lease uid. Bind with exactly that owner
	// and mode, rather than exposing a world-writable host socket.
	info, err := os.Stat(filepath.Join(root, fcvm.VsockUDSSocketName))
	mustCustomerJob(t, err)
	ownership, ok := info.Sys().(*syscall.Stat_t)
	if !ok || ownership.Uid < fcvm.JailUIDBase || ownership.Uid > fcvm.JailUIDMax {
		t.Fatal("jail ownership unavailable")
	}
	mustCustomerJob(t, os.Chown(socket, int(ownership.Uid), int(ownership.Gid)))
	mustCustomerJob(t, os.Chmod(socket, 0600))
	target, err := url.Parse(f.h.APIDURL)
	mustCustomerJob(t, err)
	relay := &customerJobRelay{socket: socket}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ModifyResponse = func(response *http.Response) error {
		if strings.HasSuffix(response.Request.URL.Path, "/result") && response.StatusCode >= 200 && response.StatusCode < 300 {
			relay.prepared.Store(true)
		}
		if loseArtifactAck && (strings.HasSuffix(response.Request.URL.Path, "/artifacts") || strings.HasSuffix(response.Request.URL.Path, "/artifact-uploads")) && response.StatusCode >= 200 && response.StatusCode < 300 && relay.lostAck.CompareAndSwap(false, true) {
			// The production handler has already retained the verified private copy.
			_ = response.Body.Close()
			f.sourceMissing.Store(true)
			return errors.New("injected lost private file acknowledgement")
		}
		return nil
	}
	prefix := "/v1/runtime/job-operations/" + id + "/"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/fixture/release" {
			if relay.release.Load() {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusAccepted)
			}
			return
		}
		action := strings.TrimPrefix(r.URL.Path, prefix)
		allowed := r.Method == http.MethodGet && action == "control" || r.Method == http.MethodPost && (action == "progress" || action == "result" || action == "artifacts" || action == "artifact-receipts" || action == "artifact-uploads" || action == "artifact-upload-receipts")
		if !strings.HasPrefix(r.URL.Path, prefix) || !allowed || r.Header.Get("Authorization") != "" {
			http.NotFound(w, r)
			return
		}
		if action == "artifact-receipts" || action == "artifact-upload-receipts" {
			relay.receiptRequests.Add(1)
		}
		generation, _ := strconv.Atoi(r.Header.Get(api.OperationGenerationHeader))
		attempt, _ := strconv.Atoi(r.Header.Get(api.OperationAttemptHeader))
		relay.mu.Lock()
		relay.observed = api.OperationJobRuntimeProof{RunID: r.Header.Get(api.OperationJobRunHeader), InstanceID: r.Header.Get(api.OperationJobInstanceHeader), Generation: generation, Attempt: attempt, Capability: r.Header.Get(api.OperationJobCapabilityHeader)}
		relay.mu.Unlock()
		proxy.ServeHTTP(w, r)
	})
	relay.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	f.relays = append(f.relays, relay)
	go func() { _ = relay.server.Serve(listener) }()
	return relay, run
}
func (r *customerJobRelay) proof() api.OperationJobRuntimeProof {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observed
}
func (r *customerJobRelay) close() {
	if r == nil {
		return
	}
	_ = r.server.Close()
	_ = os.Remove(r.socket)
}
