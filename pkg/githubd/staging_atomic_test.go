// adr: 050
// adr: 092
package githubd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceRestagingPreservesArchiveAlreadyOpenByBuilder(t *testing.T) {
	for _, repository := range []bool{false, true} {
		name := "subtree"
		if repository {
			name = "repository"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "source.tar.gz")
			write := func(content string) error {
				tree := fstest.MapFS{"app.go": &fstest.MapFile{Data: []byte(content)}}
				if repository {
					return RepackageRepositoryTree(t.Context(), tree, archive)
				}
				return RepackageRootTree(t.Context(), tree, "", archive)
			}
			if err := write("original source"); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := os.Open(archive) //nolint:forbidigo // Read a generated archive under t.TempDir, as a concurrent builder would.
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if err := write("replacement source"); err != nil {
				t.Fatal(err)
			}
			stillOpen, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(stillOpen, original) {
				t.Errorf("restaging changed the archive already open by a builder: err=%v", err)
			}
			if got := string(decodeTarball(t, archive)["app.go"]); got != "replacement source" {
				t.Errorf("published source = %q, want replacement source", got)
			}
			info, err := os.Stat(archive)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o040 == 0 {
				t.Error("published archive is unreadable by builderd's shared faas group")
			}
			assertOnlyPublishedArchive(t, dir)
		})
	}
}

func TestCanceledSourceRestagingPreservesPublishedArchive(t *testing.T) {
	service := &Service{WorkDir: t.TempDir()}
	app := state.App{ID: "app-1", AccountID: "account-1"}
	project := state.Project{AccountID: app.AccountID, RepoFullName: "org/repo"}
	tree := &stubSourceTree{fsys: stagingFixture()}
	path, _, _, err := service.stageAppSource(t.Context(), tree, app, project, "commit-sha", "main")
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := service.stageAppSource(ctx, tree, app, project, "commit-sha", "main"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restaging = %v, want context.Canceled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Errorf("failed retry removed or changed the already published archive: err=%v", err)
	}
	assertOnlyPublishedArchive(t, filepath.Dir(path))
}

func assertOnlyPublishedArchive(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "source.tar.gz" {
		t.Errorf("staging directory = %v, want only source.tar.gz", entries)
	}
}
