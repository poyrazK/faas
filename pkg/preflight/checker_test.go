package preflight

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// The happy path end to end: resolve the default branch to a commit, fetch
// that commit's tree, and report a verdict pinned to the SHA so the permalink
// is stable.
func TestChecker_ReportsGreenForCleanRepo(t *testing.T) {
	srv := newGitHubStub(t, map[string][]byte{
		"package.json": []byte(`{"name":"api","scripts":{"start":"node server.js"}}`),
		"server.js":    []byte("require('http').createServer().listen(process.env.PORT,'0.0.0.0')\n"),
	})
	defer srv.Close()

	report, err := newTestChecker(t, srv).Check(context.Background(), Source{Owner: "gregale", Repo: "api"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if report.Verdict.Level != LevelGreen {
		t.Errorf("Level = %q, want %q (findings %+v)", report.Verdict.Level, LevelGreen, report.Verdict.Findings)
	}
	if report.CommitSHA != testSHA {
		t.Errorf("CommitSHA = %q, want %q", report.CommitSHA, testSHA)
	}
	if len(report.PlanBudgets) == 0 {
		t.Error("report carries no plan budgets; the cost question goes unanswered")
	}
}

// The no path is the feature. A repository that expects durable local disk
// must come back red, with the file that proves it.
func TestChecker_ReportsRedForDurableDiskRepo(t *testing.T) {
	srv := newGitHubStub(t, map[string][]byte{
		"Dockerfile":   []byte("FROM node:22\nVOLUME /var/lib/data\nCMD [\"node\",\"s.js\"]\n"),
		"package.json": []byte(`{"name":"api","scripts":{"start":"node s.js"}}`),
	})
	defer srv.Close()

	report, err := newTestChecker(t, srv).Check(context.Background(), Source{Owner: "gregale", Repo: "api"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if report.Verdict.Level != LevelRed {
		t.Fatalf("Level = %q, want %q", report.Verdict.Level, LevelRed)
	}
	for _, f := range report.Verdict.Findings {
		if f.Code == "durable_local_disk" && f.Remedy != "" {
			return
		}
	}
	t.Fatalf("no actionable durable_local_disk finding: %+v", report.Verdict.Findings)
}

func newTestChecker(t *testing.T, srv *httptest.Server) *Checker {
	t.Helper()
	checker := NewChecker(t.TempDir())
	checker.apiBaseURL = srv.URL
	checker.codeloadBaseURL = srv.URL
	checker.httpClient = srv.Client()
	return checker
}

// newGitHubStub serves the two upstream endpoints a check needs: the commit
// lookup and the archive download.
func newGitHubStub(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/commits/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sha":"` + testSHA + `"}`))
			return
		}
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(archiveOf(t, files))
	}))
}

func archiveOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: "gregale-api-0123456/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}
