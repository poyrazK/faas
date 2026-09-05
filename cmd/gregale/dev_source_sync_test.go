package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeployDeveloperSourceRetriesFullOnMissingBase(t *testing.T) {
	var bases []string
	request := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request++
		if r.URL.Path != "/v1/apps/dev-app/deployments/dev-source" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		base := ""
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(part)
			if part.FormName() == "dev_source_base" {
				base = string(body)
			}
		}
		bases = append(bases, base)
		if request == 2 {
			api.WriteProblem(w, api.ErrDevSourceBaseMissing())
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "deployment"})
	}))
	defer srv.Close()

	source := t.TempDir()
	archiveDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.js"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stable := make([]byte, 32<<10)
	if _, err := rand.Read(stable); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "stable.bin"), stable, 0o600); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(archiveDir, "first.tar.gz")
	if _, err := packDirToTarGz(source, first, defaultZeroConfigSourceCapMB, nil); err != nil {
		t.Fatal(err)
	}
	state := &devSourceSyncState{}
	client := NewClientWithDeployTimeout(srv.URL, "token", 0)
	if _, err := deployDeveloperSource(client, context.Background(), "dev-app", first, "", "", false, "", api.DeployAnnotations{}, state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.js"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(archiveDir, "second.tar.gz")
	if _, err := packDirToTarGz(source, second, defaultZeroConfigSourceCapMB, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := deployDeveloperSource(client, context.Background(), "dev-app", second, "", "", false, "", api.DeployAnnotations{}, state); err != nil {
		t.Fatal(err)
	}
	if len(bases) != 3 || bases[0] != "" || bases[1] == "" || bases[2] != "" {
		t.Fatalf("base revision sequence = %#v, want full, delta, full retry", bases)
	}
}
