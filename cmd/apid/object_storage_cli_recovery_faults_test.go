package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// Only the local loopback response is interrupted. Provider dispatch, journal
// settlement and native GCS TLS traffic execute through the actual gateway.
type objectCLIRecoveryFaults struct {
	mu             sync.Mutex
	holdPart       int
	partSettled    chan int
	releasePart    chan struct{}
	dropCompletion bool
	partCalls      map[string]map[int]int
	completeCalls  map[string]int
}

func (f *objectCLIRecoveryFaults) gateway(w http.ResponseWriter, r *http.Request, next http.Handler) {
	id := r.URL.Query().Get("uploadId")
	part, _ := strconv.Atoi(r.URL.Query().Get("partNumber"))
	f.mu.Lock()
	if r.Method == http.MethodPut && id != "" && part > 0 {
		if f.partCalls == nil {
			f.partCalls = make(map[string]map[int]int)
		}
		if f.partCalls[id] == nil {
			f.partCalls[id] = make(map[int]int)
		}
		f.partCalls[id][part]++
	}
	hold := r.Method == http.MethodPut && part > 0 && part == f.holdPart
	ready, release := f.partSettled, f.releasePart
	if hold {
		f.holdPart = 0
	}
	f.mu.Unlock()
	if !hold {
		next.ServeHTTP(w, r)
		return
	}
	recorded := httptest.NewRecorder()
	next.ServeHTTP(recorded, r)
	ready <- recorded.Code
	<-release
	closeObjectCLIResponse(w)
}

func (f *objectCLIRecoveryFaults) control(w http.ResponseWriter, r *http.Request, status int) bool {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/complete") || !strings.Contains(r.URL.Path, "/multipart-uploads/") {
		return false
	}
	components := strings.Split(strings.TrimSuffix(r.URL.Path, "/complete"), "/")
	id := components[len(components)-1]
	f.mu.Lock()
	if f.completeCalls == nil {
		f.completeCalls = make(map[string]int)
	}
	f.completeCalls[id]++
	drop := f.dropCompletion && status == http.StatusOK
	if drop {
		f.dropCompletion = false
	}
	f.mu.Unlock()
	if drop {
		// Send an incomplete response body so http.Transport cannot treat the
		// EOF as an idle-connection failure and transparently replay the POST.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"state":`))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		closeObjectCLIResponse(w)
	}
	return drop
}

func (f *objectCLIRecoveryFaults) counts(id string) (map[int]int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := make(map[int]int)
	for part, calls := range f.partCalls[id] {
		parts[part] = calls
	}
	return parts, f.completeCalls[id]
}

func closeObjectCLIResponse(w http.ResponseWriter) {
	if hijacker, ok := w.(http.Hijacker); ok {
		connection, _, err := hijacker.Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}
}
