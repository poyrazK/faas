//go:build metal

// adr: 671
package e2e

import (
	"context"
	"errors"
	"fmt"
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
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type customerWorkflowProof struct {
	proof api.OperationWorkflowRuntimeProof
	token string
}
type customerWorkflowObservation struct {
	entered                     map[string]int
	proof                       customerWorkflowProof
	uploads, lookups            int
	lostAck, prepared, released bool
}
type customerWorkflowRelay struct {
	f        *customerWorkflowFixture
	mu       sync.Mutex
	observed map[string]*customerWorkflowObservation
	servers  map[string]*http.Server
	cancel   context.CancelFunc
	done     chan struct{}
}

func newCustomerWorkflowRelay(f *customerWorkflowFixture) *customerWorkflowRelay {
	return &customerWorkflowRelay{f: f, observed: map[string]*customerWorkflowObservation{}, servers: map[string]*http.Server{}, done: make(chan struct{})}
}

func (r *customerWorkflowRelay) watch(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	r.cancel = cancel
	go func() {
		defer close(r.done)
		for {
			instances, err := r.f.store.ListInstancesForApp(ctx, r.f.app.ID)
			if err == nil {
				for _, instance := range instances {
					sockets, _ := filepath.Glob(filepath.Join(fcvm.JailChrootBase, "firecracker*", instance.ID, "root", fcvm.VsockUDSSocketName))
					for _, socket := range sockets {
						if _, exists := r.servers[socket]; !exists {
							if err := r.bind(socket, instance.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
								t.Errorf("workflow relay bind: %v", err)
							}
						}
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
}

func (r *customerWorkflowRelay) bind(vsock, instance string) error {
	info, err := os.Stat(vsock)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || owner.Uid < fcvm.JailUIDBase || owner.Uid > fcvm.JailUIDMax {
		return fmt.Errorf("native jail ownership unavailable")
	}
	path := vsock + "_19041"
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chown(path, int(owner.Uid), int(owner.Gid)); err != nil {
		_ = listener.Close()
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return err
	}
	target, err := url.Parse(r.f.h.APIDURL)
	if err != nil {
		_ = listener.Close()
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ModifyResponse = func(response *http.Response) error {
		if !strings.HasSuffix(response.Request.URL.Path, "/artifact-uploads") || response.StatusCode != http.StatusOK {
			return nil
		}
		id := strings.Split(response.Request.URL.Path, "/")[4]
		r.mu.Lock()
		record := r.record(id)
		record.prepared = true
		lost := !record.lostAck
		record.lostAck = true
		r.mu.Unlock()
		if lost {
			_ = response.Body.Close()
			return errors.New("injected lost workflow upload acknowledgement")
		}
		return nil
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { r.forward(w, request, instance, proxy) }), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	r.servers[vsock] = server
	go func() { _ = server.Serve(listener) }()
	return nil
}

func (r *customerWorkflowRelay) record(id string) *customerWorkflowObservation {
	if r.observed[id] == nil {
		r.observed[id] = &customerWorkflowObservation{entered: map[string]int{}}
	}
	return r.observed[id]
}

func (r *customerWorkflowRelay) forward(w http.ResponseWriter, request *http.Request, instance string, proxy *httputil.ReverseProxy) {
	token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	claims, err := r.f.verifier.Verify(token, workloadidentity.OperationsAudience, time.Now())
	if err != nil || claims.AccountID != r.f.account.ID || claims.AppID != r.f.app.ID || claims.InstanceID != instance {
		http.Error(w, "native workload required", http.StatusForbidden)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "fixture" && request.Method == http.MethodGet {
		r.barrier(w, request, parts)
		return
	}
	if len(parts) != 5 || strings.Join(parts[:3], "/") != "v1/runtime/workflow-operations" {
		http.NotFound(w, request)
		return
	}
	id, action := parts[3], parts[4]
	allowed := request.Method == http.MethodGet && action == "control" || request.Method == http.MethodPost && (action == "artifact-uploads" || action == "artifact-upload-receipts")
	if !r.f.ownsOperation(request.Context(), id) || !allowed {
		http.NotFound(w, request)
		return
	}
	generation, _ := strconv.Atoi(request.Header.Get(api.OperationGenerationHeader))
	attempt, _ := strconv.Atoi(request.Header.Get(api.OperationAttemptHeader))
	r.mu.Lock()
	record := r.record(id)
	if request.Header.Get(api.OperationWorkflowStepHeader) == "finish" {
		record.proof = customerWorkflowProof{proof: api.OperationWorkflowRuntimeProof{RunID: request.Header.Get(api.OperationWorkflowRunHeader), StepName: "finish", Generation: generation, Attempt: attempt, Capability: request.Header.Get(api.OperationWorkflowCapabilityHeader)}, token: token}
	}
	if action == "artifact-uploads" {
		record.uploads++
	}
	if action == "artifact-upload-receipts" {
		record.lookups++
	}
	r.mu.Unlock()
	proxy.ServeHTTP(w, request)
}

func (f *customerWorkflowFixture) ownsOperation(ctx context.Context, id string) bool {
	f.mu.Lock()
	known := f.operationIDs[id]
	f.mu.Unlock()
	if known {
		return true
	}
	op, err := f.store.OperationByID(ctx, f.account.ID, f.tenantID, id)
	return err == nil && op.AppID == f.app.ID
}

func (r *customerWorkflowRelay) barrier(w http.ResponseWriter, request *http.Request, parts []string) {
	if !r.f.ownsOperation(request.Context(), parts[2]) {
		http.NotFound(w, request)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.record(parts[2])
	switch parts[1] {
	case "entered":
		record.entered[parts[3]]++
		w.WriteHeader(http.StatusNoContent)
	case "release":
		if record.released {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
	default:
		http.NotFound(w, request)
	}
}

func (r *customerWorkflowRelay) observation(id string) customerWorkflowObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.record(id)
	copy := *record
	copy.entered = map[string]int{}
	for key, value := range record.entered {
		copy.entered[key] = value
	}
	return copy
}
func (r *customerWorkflowRelay) release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(id).released = true
}
func (r *customerWorkflowRelay) close() {
	if r.cancel != nil {
		r.cancel()
		<-r.done
	}
	for path, server := range r.servers {
		_ = server.Close()
		_ = os.Remove(path + "_19041")
	}
}
