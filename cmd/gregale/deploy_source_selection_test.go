package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDeploySourceSelection(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		worktree      bool
		image         string
		archive       string
		repo          string
		template      string
		githubSnippet bool
		ref           string
		want          string
	}{
		{name: "zero config"},
		{name: "repository", repo: "owner/repo", ref: "main"},
		{name: "orphan ref", ref: "main", want: "--ref requires --repo"},
		{name: "image and archive", image: "registry/app:latest", archive: "app.tgz", want: "--image, --tarball"},
		{name: "repository and template", repo: "owner/repo", ref: "main", template: "node", want: "--repo, --template"},
		{name: "snippet and image", image: "registry/app:latest", githubSnippet: true, want: "--image, --github"},
		{name: "path with worktree contents", path: "service", worktree: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDeploySourceSelection(tt.path, tt.worktree, tt.image, tt.archive, tt.repo, tt.template, tt.githubSnippet, tt.ref)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateRepoDeployFlags(t *testing.T) {
	if err := validateRepoDeployFlags(map[string]bool{"no-triggers": true, "wait": true}); err != nil {
		t.Fatalf("supported flags rejected: %v", err)
	}
	err := validateRepoDeployFlags(map[string]bool{"app": true, "vcpu": true, "doctor-strict": true})
	if err == nil {
		t.Fatal("unsupported flags accepted")
	}
	for _, flag := range []string{"--app", "--vcpu", "--doctor-strict"} {
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("error %q does not name %s", err, flag)
		}
	}
}

func TestImageDeployHasNoImplicitManifestSource(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("workflows:\n  - name: from-cwd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateDeployDiffManifest(""); err != nil {
		t.Fatalf("image preview read cwd manifest: %v", err)
	}
	if got := previewCronsFromManifest("", "image-app"); len(got) != 0 {
		t.Fatalf("image preview imported %d cwd cron(s)", len(got))
	}
	if got, err := loadWorkflowManifestForDeploy(context.Background(), nil, ""); err != nil || len(got) != 0 {
		t.Fatalf("image deploy imported cwd workflows: got=%v err=%v", got, err)
	}
	if got, err := deployManifestTriggersWithRollback(context.Background(), nil, "image-app", ""); err != nil || len(got.steps) != 0 {
		t.Fatalf("image deploy imported cwd triggers: got=%v err=%v", got, err)
	}
}

func TestValidateExplicitDockerfile(t *testing.T) {
	dir := t.TempDir()
	if err := validateExplicitDockerfile(dir); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing Dockerfile error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExplicitDockerfile(dir); err != nil {
		t.Fatalf("valid Dockerfile rejected: %v", err)
	}
}
