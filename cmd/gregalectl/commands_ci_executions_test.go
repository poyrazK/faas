package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIExecutionRequestsKeepFreshIdentityAndBoundedPaths(t *testing.T) {
	o := ciExecutionOptions{id: uuid.NewString(), revision: uuid.NewString(), commit: strings.Repeat("a", 40), key: "fresh-execution", binding: uuid.NewString(), templates: ciTemplates{"test": uuid.NewString()}, attempt: uuid.NewString(), limit: 4, job: "test", artifact: "reports/unit file.txt", output: "report.txt"}
	method, path, body, headers, err := ciExecutionRequest("submit", o)
	request, ok := body.(ci.JobsSubmitRequest)
	if err != nil || !ok || method != http.MethodPost || path != "/v1/internal/ci/executions" || request.BindingID != o.binding || request.Templates["test"] != o.templates["test"] || headers.Get("Idempotency-Key") != o.key {
		t.Fatal("execution request lost fresh immutable identity")
	}
	_, path, _, _, err = ciExecutionRequest("artifact", o)
	if err != nil || !strings.HasSuffix(path, "/reports/unit%20file.txt") {
		t.Fatal("artifact URL was not escaped per path segment")
	}
	method, path, body, headers, err = ciExecutionRequest("reports", ciExecutionOptions{id: o.id})
	if err != nil || method != http.MethodGet || path != "/v1/internal/ci/executions/"+o.id+"/reports" || body != nil || len(headers) != 0 {
		t.Fatal("report read requested compute work or lost execution scope")
	}
	if _, _, _, _, err := ciExecutionRequest("reports", ciExecutionOptions{id: "../outside"}); err == nil {
		t.Fatal("report read accepted an invalid execution identity")
	}
	for _, name := range []string{"", "/absolute", "../outside", "reports/../outside", "reports//file", "reports\\file", "reports/\nfile"} {
		if validCIArtifactPath(name) {
			t.Fatalf("unsafe artifact path accepted: %q", name)
		}
	}
	for _, raw := range []string{"test=invalid", "../test=" + uuid.NewString(), "test=" + uuid.NewString()} {
		templates := ciTemplates{"test": uuid.NewString()}
		if err := templates.Set(raw); err == nil {
			t.Fatal("invalid or duplicate template accepted")
		}
	}
	o.key = ""
	if _, _, _, _, err := ciExecutionRequest("submit", o); err == nil {
		t.Fatal("missing admission identity accepted")
	}
	o.offset = -1
	if _, _, _, _, err := ciExecutionRequest("logs", o); err == nil {
		t.Fatal("negative log cursor accepted")
	}
}

func TestCIArtifactDownloadVerifiesBeforePublishing(t *testing.T) {
	data := []byte{0, 255, 'r', 'e', 'p', 'o', 'r', 't'}
	digest := sha256.Sum256(data)
	ref := ci.ArtifactRef{Path: "reports/unit.bin", Digest: "sha256:" + hex.EncodeToString(digest[:]), Bytes: int64(len(data))}
	for _, name := range []string{"valid", "checksum", "truncated", "oversized", "existing", "symlink", "redirect", "wrong_header"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			destination := filepath.Join(dir, "report.bin")
			if name == "existing" {
				if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if name == "symlink" {
				if err := os.Symlink(filepath.Join(dir, "target"), destination); err != nil {
					t.Fatal(err)
				}
			}
			redirects := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cookie, err := r.Cookie("faas_sid")
				if err != nil || cookie.Value != "operator-session" {
					t.Error("download omitted operator authentication")
				}
				if strings.HasSuffix(r.URL.Path, "/outside") {
					redirects++
					return
				}
				if !strings.Contains(r.URL.Path, "/artifacts/") {
					_ = json.NewEncoder(w).Encode(ci.Execution{Jobs: []ci.JobExecution{{Name: "test", Attempt: &ci.Attempt{Artifacts: []ci.ArtifactRef{ref}}}}})
					return
				}
				if name == "redirect" {
					http.Redirect(w, r, "/outside", http.StatusTemporaryRedirect)
					return
				}
				w.Header().Set(ci.RuntimeDigestHeader, ref.Digest)
				if name == "wrong_header" {
					w.Header().Set(ci.RuntimeDigestHeader, "sha256:"+strings.Repeat("0", 64))
				}
				switch name {
				case "checksum":
					_, _ = w.Write(bytes.Repeat([]byte{'x'}, len(data)))
				case "truncated":
					_, _ = w.Write(data[:2])
				case "oversized":
					_, _ = w.Write(append(data, 'x'))
				default:
					_, _ = w.Write(data)
				}
			}))
			defer server.Close()
			client := newOperatorHTTPClient(&operatorSession{BaseURL: server.URL, Cookie: "operator-session"})
			o := ciExecutionOptions{id: uuid.NewString(), job: "test", artifact: ref.Path, output: destination}
			err := client.downloadCIArtifact(t.Context(), o, "/v1/internal/ci/executions/"+o.id+"/jobs/test/artifacts/"+ref.Path)
			if name == "valid" {
				actual, readErr := os.ReadFile(destination)
				if err != nil || readErr != nil || !bytes.Equal(actual, data) {
					t.Fatalf("valid artifact failed: %v %v", err, readErr)
				}
				info, _ := os.Stat(destination)
				if info.Mode().Perm() != 0600 {
					t.Fatal("download is not private")
				}
			} else if err == nil {
				t.Fatal("unverified or unsafe artifact was published")
			}
			if redirects != 0 {
				t.Fatal("download followed a redirect with operator credentials")
			}
			files, _ := filepath.Glob(filepath.Join(dir, ".gregale-ci-artifact-*"))
			if len(files) != 0 {
				t.Fatal("download left partial files")
			}
		})
	}
	// Same headers cannot make corrupted bytes acceptable.
	if err := saveCIArtifact(io.LimitReader(strings.NewReader("wrong"), 3), filepath.Join(t.TempDir(), "bad"), ref); err == nil {
		t.Fatal("partial bytes passed checksum verification")
	}
}
