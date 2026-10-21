//go:build metal

// adr: 521
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

// The guest's handler directive: hold (202), release (204), or exit the real
// guest process after private retention and before replying (205).
const (
	customerHTTPHold    = http.StatusAccepted
	customerHTTPRelease = http.StatusNoContent
	customerHTTPCrash   = http.StatusResetContent
)

type customerHTTPProof struct {
	proof api.OperationRuntimeProof
	token string
}

type customerHTTPObservation struct {
	// entered counts handler entries per real invocation (one per generation).
	entered          map[string]int
	proof            customerHTTPProof
	uploads, lookups int
	lostAck          bool
	directive        int
}

// customerHTTPRelay is a test-only per-instance vsock relay to loopback apid.
// It verifies the real vmmd-minted workload assertion, forwards only the
// ordinary HTTP runtime routes and injects one lost upload acknowledgement
// after the real API has retained the file. It never mints authority, records
// success or settles an operation.
type customerHTTPRelay struct {
	f        *customerHTTPFixture
	mu       sync.Mutex
	observed map[string]*customerHTTPObservation
	servers  map[string]*http.Server
	cancel   context.CancelFunc
	done     chan struct{}
}

func newCustomerHTTPRelay(f *customerHTTPFixture) *customerHTTPRelay {
	return &customerHTTPRelay{f: f, observed: map[string]*customerHTTPObservation{}, servers: map[string]*http.Server{}, done: make(chan struct{})}
}

func (r *customerHTTPRelay) watch(t *testing.T) {
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
								t.Errorf("HTTP operation relay bind: %v", err)
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

func (r *customerHTTPRelay) bind(vsock, instance string) error {
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
		lost := !record.lostAck
		record.lostAck = true
		r.mu.Unlock()
		if lost {
			_ = response.Body.Close()
			return errors.New("injected lost HTTP operation upload acknowledgement")
		}
		return nil
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { r.forward(w, request, instance, proxy) }), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	r.servers[vsock] = server
	go func() { _ = server.Serve(listener) }()
	return nil
}

func (r *customerHTTPRelay) record(id string) *customerHTTPObservation {
	if r.observed[id] == nil {
		r.observed[id] = &customerHTTPObservation{entered: map[string]int{}, directive: customerHTTPHold}
	}
	return r.observed[id]
}

func (r *customerHTTPRelay) forward(w http.ResponseWriter, request *http.Request, instance string, proxy *httputil.ReverseProxy) {
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
	if len(parts) != 5 || strings.Join(parts[:3], "/") != "v1/runtime/operations" {
		http.NotFound(w, request)
		return
	}
	id, action := parts[3], parts[4]
	allowed := request.Method == http.MethodGet && action == "control" || request.Method == http.MethodPost && (action == "progress" || action == "artifact-uploads" || action == "artifact-upload-receipts")
	if !r.f.ownsOperation(request.Context(), id) || !allowed {
		http.NotFound(w, request)
		return
	}
	attempt, _ := strconv.Atoi(request.Header.Get(api.OperationAttemptHeader))
	r.mu.Lock()
	record := r.record(id)
	record.proof = customerHTTPProof{proof: api.OperationRuntimeProof{InvocationID: request.Header.Get(api.InvocationIDHeader), Attempt: attempt, Capability: request.Header.Get(api.OperationCapabilityHeader)}, token: token}
	switch action {
	case "artifact-uploads":
		record.uploads++
	case "artifact-upload-receipts":
		record.lookups++
	}
	r.mu.Unlock()
	proxy.ServeHTTP(w, request)
}

func (f *customerHTTPFixture) ownsOperation(ctx context.Context, id string) bool {
	f.mu.Lock()
	known := f.operationIDs[id]
	f.mu.Unlock()
	if known {
		return true
	}
	op, err := f.store.OperationByID(ctx, f.account.ID, f.tenantID, id)
	return err == nil && op.AppID == f.app.ID
}

// barrier records handler entry and serves the current directive. It never
// records successful work; only the real API settles the operation.
func (r *customerHTTPRelay) barrier(w http.ResponseWriter, request *http.Request, parts []string) {
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
		w.WriteHeader(record.directive)
	default:
		http.NotFound(w, request)
	}
}

func (r *customerHTTPRelay) observation(id string) customerHTTPObservation {
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

func (r *customerHTTPRelay) direct(id string, directive int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record(id).directive = directive
}

func (r *customerHTTPRelay) close() {
	if r.cancel != nil {
		r.cancel()
		<-r.done
	}
	for path, server := range r.servers {
		_ = server.Close()
		_ = os.Remove(path + "_19041")
	}
}
