// adr: 375
package vmmdgrpc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

type nodeDeadlineObservation struct {
	Claims          map[string]trafficdeadline.Claims
	ClaimErrors     map[string]string
	ChildFinishedNS int64
	ChildStatus     int
	ChildError      bool
}

type nodeDeadlineFixture struct {
	mu     sync.Mutex
	target string
	signer *trafficdeadline.Signer
	seen   nodeDeadlineObservation
}

func newNodeDeadlineFixture() *nodeDeadlineFixture {
	master, _ := hex.DecodeString(os.Getenv("FAAS_SESSION_KEY"))
	signer, _ := trafficdeadline.New(master, nil)
	return &nodeDeadlineFixture{signer: signer, seen: nodeDeadlineObservation{
		Claims: make(map[string]trafficdeadline.Claims), ClaimErrors: make(map[string]string)}}
}

func (f *nodeDeadlineFixture) configure(w http.ResponseWriter, r *http.Request) {
	var target string
	if err := json.NewDecoder(r.Body).Decode(&target); err != nil {
		http.Error(w, "invalid fixture endpoint", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.target = target
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *nodeDeadlineFixture) observation() nodeDeadlineObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	copy := f.seen
	copy.Claims, copy.ClaimErrors = make(map[string]trafficdeadline.Claims), make(map[string]string)
	for path, claim := range f.seen.Claims {
		copy.Claims[path] = claim
	}
	for path, err := range f.seen.ClaimErrors {
		copy.ClaimErrors[path] = err
	}
	return copy
}

func (f *nodeDeadlineFixture) serveGuest(w http.ResponseWriter, r *http.Request) bool {
	f.recordClaim(r)
	if r.URL.Path != "/chain" {
		return false
	}
	// Explicit context propagation is application responsibility. The fixture
	// copies the opaque carrier but deliberately does not inherit r.Context:
	// the downstream gateway must enforce it independently of caller cleanup.
	time.Sleep(300 * time.Millisecond)
	f.mu.Lock()
	target := f.target
	f.mu.Unlock()
	f.forwardChain(w, r, target)
	return true
}

func (f *nodeDeadlineFixture) recordClaim(r *http.Request) {
	if token := r.Header.Get(trafficdeadline.Header); token != "" {
		claim, err := f.signer.Authenticate(token)
		f.mu.Lock()
		if err != nil {
			f.seen.ClaimErrors[r.URL.Path] = err.Error()
		} else {
			f.seen.Claims[r.URL.Path] = claim
		}
		f.mu.Unlock()
	}
}

func (f *nodeDeadlineFixture) forwardChain(w http.ResponseWriter, r *http.Request, target string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"/v1/internal/services/fleet-retry/hold", nil)
	if err != nil {
		http.Error(w, "invalid fixture chain", http.StatusBadGateway)
		return
	}
	request.Header.Set(trafficdeadline.Header, r.Header.Get(trafficdeadline.Header))
	response, err := http.DefaultClient.Do(request)
	status := http.StatusBadGateway
	var body []byte
	if response != nil {
		status = response.StatusCode
		body, err = io.ReadAll(response.Body)
		_ = response.Body.Close()
	}
	f.mu.Lock()
	f.seen.ChildStatus, f.seen.ChildError, f.seen.ChildFinishedNS = status, err != nil, time.Now().UnixNano()
	f.mu.Unlock()
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
