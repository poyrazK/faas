// Checks API writer tests (slice 8, ADR-012).
package githubd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

func TestChecksAPI_PhaseMapping(t *testing.T) {
	cases := []struct {
		phase      githubdgrpc.CheckPhase
		wantStatus string
		wantConcl  string
		wantTitle  string
	}{
		{githubdgrpc.CheckPhaseQueued, "queued", "", "Build queued"},
		{githubdgrpc.CheckPhaseBuilding, "in_progress", "", "Build in progress"},
		{githubdgrpc.CheckPhaseLive, "completed", "success", "Deployment live"},
		{githubdgrpc.CheckPhaseFailed, "completed", "failure", "Deployment failed"},
	}
	for _, c := range cases {
		if got := phaseToStatus(c.phase); got != c.wantStatus {
			t.Errorf("phase %v: status = %q, want %q", c.phase, got, c.wantStatus)
		}
		if got := phaseToConclusion(c.phase); got != c.wantConcl {
			t.Errorf("phase %v: conclusion = %q, want %q", c.phase, got, c.wantConcl)
		}
		if got := phaseTitle(c.phase); got != c.wantTitle {
			t.Errorf("phase %v: title = %q, want %q", c.phase, got, c.wantTitle)
		}
	}
}

func TestChecksAPI_WriteCheck_HTTP(t *testing.T) {
	var hits atomic.Int32
	var gotBody map[string]any
	var gotAuth string
	var gotPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12345}`))
	}))
	defer fake.Close()

	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		return "ghs_test_token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, &fakeBindings{id: 42})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCheck(context.Background(), "octo/api", "deadbeef", githubdgrpc.CheckPhaseQueued, "https://example.test/logs", "queued"); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
	if !strings.Contains(gotPath, "/repos/octo/api/check-runs") {
		t.Errorf("path = %q, want /repos/octo/api/check-runs", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("auth = %q, want Bearer prefix", gotAuth)
	}
	if gotBody["head_sha"] != "deadbeef" {
		t.Errorf("head_sha = %v, want deadbeef", gotBody["head_sha"])
	}
	if gotBody["status"] != "queued" {
		t.Errorf("status = %v, want queued", gotBody["status"])
	}
}

func TestChecksAPI_RejectsMissingArgs(t *testing.T) {
	c := &ChecksAPI{HTTP: http.DefaultClient}
	if err := c.WriteCheck(context.Background(), "", "sha", githubdgrpc.CheckPhaseQueued, "", ""); err == nil {
		t.Error("empty repo should error")
	}
	if err := c.WriteCheck(context.Background(), "owner/repo", "", githubdgrpc.CheckPhaseQueued, "", ""); err == nil {
		t.Error("empty sha should error")
	}
}

// _ keeps imports stable for future slices that add HTTPClient mocks.
var _ HTTPClient = (*http.Client)(nil)

// fakeBindings is the test stub for BindingsLookup. Returns a fixed
// install id by default; tests that need to simulate "no app
// bound" can construct it with id=0 (tokensForRepo will fail at
// the token-cache step).
type fakeBindings struct {
	id    int64
	err   error
	hits  atomic.Int32
	gotRepo string
}

func (f *fakeBindings) InstallationIDForRepo(_ context.Context, repoFullName string) (int64, error) {
	f.hits.Add(1)
	f.gotRepo = repoFullName
	return f.id, f.err
}

// TestWriteCheck_UsesBindingLookup is the regression test for
// review finding #1+#2: checks must go out under the per-repo
// installation's token, not the hardcoded installation_id=1.
// The fake fetcher records which install id it was called with; we
// assert it matches the binding lookup's id, not a constant.
func TestWriteCheck_UsesBindingLookup(t *testing.T) {
	var fetchedInstall atomic.Int64
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer fake.Close()

	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, id int64) (string, time.Time, error) {
		fetchedInstall.Store(id)
		return "ghs_test", time.Now().Add(time.Hour), nil
	}), time.Minute)
	b := &fakeBindings{id: 9876}
	c, err := NewChecksAPI(tokens, &singleHostClient{base: fake.Client(), api: fake.URL}, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteCheck(context.Background(), "octo/api", "deadbeef", githubdgrpc.CheckPhaseQueued, "", "queued"); err != nil {
		t.Fatal(err)
	}
	if fetchedInstall.Load() != 9876 {
		t.Errorf("fetched install = %d, want 9876 (the binding lookup id, not 1)", fetchedInstall.Load())
	}
	if b.gotRepo != "octo/api" {
		t.Errorf("lookup repo = %q, want octo/api", b.gotRepo)
	}
}

// TestWriteCheck_NoBindingFailsClosed asserts the §11 fail-closed
// behavior: when no app is bound to the repo, WriteCheck returns
// an error instead of falling back to installation_id=1 (which
// would send another customer's check-run under the wrong install).
func TestWriteCheck_NoBindingFailsClosed(t *testing.T) {
	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, _ int64) (string, time.Time, error) {
		t.Fatal("token cache must not be hit when bindings lookup misses")
		return "", time.Time{}, nil
	}), time.Minute)
	c, err := NewChecksAPI(tokens, http.DefaultClient, &fakeBindings{err: ErrNoBinding})
	if err != nil {
		t.Fatal(err)
	}
	err = c.WriteCheck(context.Background(), "octo/api", "deadbeef", githubdgrpc.CheckPhaseQueued, "", "queued")
	if err == nil {
		t.Fatal("expected error when no app is bound, got nil")
	}
	if !strings.Contains(err.Error(), "no app bound") {
		t.Errorf("err = %v, want 'no app bound' message", err)
	}
}
