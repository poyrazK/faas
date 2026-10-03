package preflight

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHandler(t *testing.T, stub *httptest.Server) *Handler {
	t.Helper()
	return NewHandler(newTestChecker(t, stub))
}

// A valid public repository returns the report, with the commit it was
// computed from so the answer can be pinned.
func TestHandler_ReturnsReportForValidSource(t *testing.T) {
	stub := newGitHubStub(t, map[string][]byte{
		"package.json": []byte(`{"name":"api","scripts":{"start":"node s.js"}}`),
	})
	defer stub.Close()

	rec := httptest.NewRecorder()
	newTestHandler(t, stub).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/preflight?source=gregale/api", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var report Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.CommitSHA != testSHA {
		t.Errorf("CommitSHA = %q, want %q", report.CommitSHA, testSHA)
	}
	if report.Verdict.Level != LevelGreen {
		t.Errorf("Level = %q, want green", report.Verdict.Level)
	}
}

// Hostile or malformed input is refused with a stable RFC 7807 code, never a
// bare status the client has to guess at.
func TestHandler_RejectsHostileSourceWithStableCode(t *testing.T) {
	stub := newGitHubStub(t, nil)
	defer stub.Close()
	handler := newTestHandler(t, stub)

	for _, source := range []string{"http://169.254.169.254/latest/", "https://evil.example/a/b", ""} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/preflight?source="+source, nil))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("source %q: status = %d, want 422", source, rec.Code)
			continue
		}
		var problem struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Errorf("source %q: decode problem: %v", source, err)
			continue
		}
		if problem.Code != "preflight_invalid_source" {
			t.Errorf("source %q: code = %q, want preflight_invalid_source", source, problem.Code)
		}
	}
}

// A second check of the same commit is served from cache, so a permalink
// costs nothing upstream. Anonymous GitHub API calls are capped at 60/hour.
func TestHandler_SecondCheckOfSameCommitDoesNotRefetch(t *testing.T) {
	var upstreamCalls int
	stub := newCountingGitHubStub(t, &upstreamCalls, map[string][]byte{
		"package.json": []byte(`{"name":"api","scripts":{"start":"node s.js"}}`),
	})
	defer stub.Close()
	handler := newTestHandler(t, stub)

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/preflight?source=gregale/api&ref="+testSHA, nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d (body %s)", i, rec.Code, rec.Body.String())
		}
	}

	if upstreamCalls > 2 {
		t.Errorf("upstream calls = %d, want at most 2 (one resolve + one archive); the second check refetched", upstreamCalls)
	}
}

func newCountingGitHubStub(t *testing.T, calls *int, files map[string][]byte) *httptest.Server {
	t.Helper()
	inner := newGitHubStub(t, files)
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		inner.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(inner.Close)
	return counting
}
