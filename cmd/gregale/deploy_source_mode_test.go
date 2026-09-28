package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResolveDeploySourceMode(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		explicit   bool
		worktree   bool
		nonLocal   bool
		createOnly bool
		want       deploySourceMode
		wantErr    string
	}{
		{name: "default", value: "auto", want: deploySourceAuto},
		{name: "legacy worktree", value: "auto", worktree: true, want: deploySourceWorktree},
		{name: "explicit head", value: "head", explicit: true, want: deploySourceHEAD},
		{name: "explicit worktree", value: "worktree", explicit: true, want: deploySourceWorktree},
		{name: "unknown", value: "branch", explicit: true, wantErr: "invalid --source"},
		{name: "conflicting spellings", value: "head", explicit: true, worktree: true, wantErr: "cannot be combined"},
		{name: "remote source", value: "head", explicit: true, nonLocal: true, wantErr: "local directories"},
		{name: "reservation", value: "head", explicit: true, createOnly: true, wantErr: "--create-only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDeploySourceMode(tt.value, tt.explicit, tt.worktree, tt.nonLocal, tt.createOnly)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("mode = %q, error = %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestDeploySourceFlagRejectsNonlocalAndMissingHEAD(t *testing.T) {
	for _, args := range [][]string{
		{"--source=head", "--image", "registry.example/app@sha256:abc"},
		{"--source=worktree", "--tarball", "source.tar.gz"},
		{"--source=head", "--repo", "acme/api", "--ref", "main"},
		{"--source=head", "--template", "hello-node"},
		{"--source=head", "--github"},
		{"--source=head", "--create-only", "--app"},
		{"--source=head", "--worktree"},
		{"--source=unknown"},
	} {
		if code := cmdDeployTarball(args); code != 1 {
			t.Errorf("cmdDeployTarball(%v) = %d, want 1", args, code)
		}
	}
	withCwd(t, t.TempDir())
	if code := cmdDeployTarball([]string{"--source=head", "--plan", "--name", "demo"}); code != 1 {
		t.Errorf("--source=head outside Git returned %d, want 1", code)
	}
}

func TestDeployExplicitHEADRequiresCommit(t *testing.T) {
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q")
	withCwd(t, repo)
	if code := cmdDeployTarball([]string{"--source=head", "--plan", "--name", "demo"}); code != 1 {
		t.Errorf("--source=head in an empty repository returned %d, want 1", code)
	}
}

func TestDeployExplicitSourceWithoutOrigin(t *testing.T) {
	repo := initZeroConfigRepo(t)
	mustGit(t, repo, "remote", "remove", "origin")
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte("{\"name\":\"dirty\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "local.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withCwd(t, repo)

	var archives [][]byte
	stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *zeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == http.MethodPost:
			reader, err := r.MultipartReader()
			if err != nil {
				t.Errorf("multipart reader: %v", err)
				return
			}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Errorf("multipart part: %v", err)
					return
				}
				if part.FormName() == "source" {
					data, readErr := io.ReadAll(part)
					if readErr != nil {
						t.Errorf("read source: %v", readErr)
					}
					archives = append(archives, data)
				}
				_ = part.Close()
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "demo"})
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	})
	t.Setenv("FAAS_API", stub.srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	for _, mode := range []string{"auto", "head", "worktree"} {
		if code := cmdDeployTarball([]string{"--source=" + mode, "--name", "demo", "--no-wait"}); code != 0 {
			t.Fatalf("--source=%s exit = %d, want 0", mode, code)
		}
	}
	if len(archives) != 3 {
		t.Fatalf("uploaded archives = %d, want 3", len(archives))
	}
	auto := readCapturedDeployArchive(t, archives[0])
	head := readCapturedDeployArchive(t, archives[1])
	worktree := readCapturedDeployArchive(t, archives[2])
	find := func(entries map[string][]byte, name string) []byte {
		for entry, body := range entries {
			if filepath.Base(entry) == name {
				return body
			}
		}
		return nil
	}
	if !bytes.Contains(find(head, "package.json"), []byte("{}")) || find(head, "local.txt") != nil {
		t.Errorf("HEAD source included local changes: %v", head)
	}
	if !bytes.Contains(find(worktree, "package.json"), []byte("dirty")) || !bytes.Equal(find(worktree, "local.txt"), []byte("untracked\n")) {
		t.Errorf("worktree source omitted local changes: %v", worktree)
	}
	if !bytes.Contains(find(auto, "package.json"), []byte("dirty")) || !bytes.Equal(find(auto, "local.txt"), []byte("untracked\n")) {
		t.Errorf("auto source changed the no-origin fallback: %v", auto)
	}
}
