package mcpmanagement

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

type countingPersistence struct {
	commits int
	err     error
}

func (p *countingPersistence) load() (map[string]json.RawMessage, error) { return nil, nil }
func (p *countingPersistence) close() error                              { return nil }
func (p *countingPersistence) commit(map[string]json.RawMessage, []string) error {
	p.commits++
	return p.err
}

// The hosted bridge rewrote its whole OAuth state after every request,
// discovery GETs included, and one failed commit latched every later request
// into 503 until a restart (bug hunt 2026-10-28 B16).
func TestStateStoreCommitsChangesOnlyAndRetriesFailures(t *testing.T) {
	persist := &countingPersistence{}
	s := &StateStore{persist: persist, records: map[string]json.RawMessage{}}
	if err := s.update("oauth", map[string]int{"a": 1}); err != nil || persist.commits != 1 {
		t.Fatalf("first update: %v, %d commits", err, persist.commits)
	}
	if err := s.update("oauth", map[string]int{"a": 1}); err != nil || persist.commits != 1 {
		t.Fatalf("unchanged update committed again: %v, %d commits", err, persist.commits)
	}
	persist.err = errors.New("CP blip")
	if err := s.update("oauth", map[string]int{"a": 2}); err == nil {
		t.Fatal("a failed commit was reported as committed")
	}
	if !s.available() {
		t.Fatal("one failed commit disabled the store")
	}
	persist.err = nil
	if err := s.update("oauth", map[string]int{"a": 2}); err != nil || persist.commits != 3 {
		t.Fatalf("the change that failed was not retried: %v, %d commits", err, persist.commits)
	}
	// A snapshot taken earlier but committed later never replaces a newer one.
	if err := s.updateSnapshot("oauth", map[string]int{"a": 4}, 9); err != nil {
		t.Fatal(err)
	}
	if err := s.updateSnapshot("oauth", map[string]int{"a": 3}, 8); err != nil {
		t.Fatal(err)
	}
	if got := string(s.records["oauth"]); got != `{"a":4}` {
		t.Fatalf("record = %s, want the newer snapshot", got)
	}
	persist.err = ErrStateLeaseLost
	_ = s.update("oauth", map[string]int{"a": 5})
	if s.available() {
		t.Fatal("a lost lease left the store writable")
	}
}

// GET /oauth/authorize created a pending flow per call with no per-address
// limit, so one address could fill the pool and refuse every real sign-in
// (bug hunt 2026-10-28 H40).
func TestHostedAuthorizeIsLimitedPerAddressAndEvictsTheOldestFlow(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{Hosted: true}, requests: map[string]*authorizationRequest{}}
	req := httptest.NewRequest("GET", "/oauth/authorize", nil)
	req.RemoteAddr = "198.51.100.7:4321"
	for i := range hostedAuthorizeLimit {
		if !r.authorizeAllowedLocked(req) {
			t.Fatalf("authorization %d refused under the limit", i)
		}
	}
	if r.authorizeAllowedLocked(req) {
		t.Fatal("authorization past the per-address limit admitted")
	}
	other := httptest.NewRequest("GET", "/oauth/authorize", nil)
	other.RemoteAddr = "203.0.113.9:4321"
	if !r.authorizeAllowedLocked(other) {
		t.Fatal("another address was refused")
	}
	now := time.Now()
	r.requests["old"] = &authorizationRequest{ID: "old", Expires: now.Add(time.Minute)}
	r.requests["new"] = &authorizationRequest{ID: "new", Expires: now.Add(4 * time.Minute)}
	r.evictOldestRequestLocked()
	if _, ok := r.requests["old"]; ok || len(r.requests) != 1 {
		t.Fatalf("eviction left %v, want only the newest flow", r.requests)
	}
}
