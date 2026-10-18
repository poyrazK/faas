package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// H10-41: `gregale deploy --secrets-file .env.prod` kept in the source tree
// uploaded the plaintext file inside the source archive.
func TestDeploySecretsFileStaysOutOfSourceArchive(t *testing.T) {
	// A plain directory, not a git repo: a repo deploy ships only tracked
	// files, so an untracked secrets file never reached the archive there.
	repo := t.TempDir()
	for name, body := range map[string]string{
		"package.json": `{"name":"demo","scripts":{"start":"node index.js"}}` + "\n",
		"index.js":     "require('http').createServer((q, s) => s.end('ok')).listen(process.env.PORT || 8080)\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	withCwd(t, repo)
	secretsPath := filepath.Join(repo, ".env.prod")
	if err := os.WriteFile(secretsPath, []byte("H10_TOKEN=4f1c9a7e22b06d38\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var archived []string
	stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *zeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo-secrets"})
		case strings.HasPrefix(r.URL.Path, "/v1/apps/demo-secrets/secrets/") && r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/apps/demo-secrets/deployments" && r.Method == http.MethodPost:
			archived = uploadedArchiveNames(t, r)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "demo-secrets"})
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	})
	t.Setenv("FAAS_API", stub.srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeployTarball([]string{"--name", "demo-secrets", "--secrets-file", secretsPath, "--no-wait"}); code != 0 {
		t.Fatalf("deploy exit = %d, want 0", code)
	}
	if len(archived) == 0 {
		t.Fatal("no source archive was uploaded")
	}
	for _, name := range archived {
		if filepath.Base(name) == ".env.prod" {
			t.Fatalf("source archive contains the secrets file: %v", archived)
		}
	}
}

func uploadedArchiveNames(t *testing.T, r *http.Request) []string {
	t.Helper()
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Errorf("deployment content type: %v", err)
		return nil
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	var names []string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Errorf("multipart: %v", err)
			return names
		}
		if part.FileName() == "" {
			continue
		}
		gz, err := gzip.NewReader(part)
		if err != nil {
			t.Errorf("gzip: %v", err)
			return names
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			names = append(names, h.Name)
		}
	}
}
