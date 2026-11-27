package tasktoolsource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tarball"
)

func TestPackReviewedArchivePreservesAuthoredTree(t *testing.T) {
	entries := []api.HostedTaskSourceEntry{
		{Path: "worker.js", Mode: 0700, Content: []byte("worker bytes")},
		{Path: "sub/nested", Mode: 0600, Content: []byte{0, 1, 2}},
		{Path: ".", Mode: 0700, Directory: true},
		{Path: "sub", Mode: 0700, Directory: true},
		{Path: "empty", Mode: 0700, Directory: true},
	}
	digest, err := Digest(entries)
	if err != nil {
		t.Fatal(err)
	}
	first, err := PackReviewedArchive(entries, digest)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(entries)
	second, err := PackReviewedArchive(entries, digest)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("archive changed with entry order: %v", err)
	}

	spool := filepath.Join(t.TempDir(), "reviewed.tar.gz")
	if err := os.WriteFile(spool, first, 0600); err != nil {
		t.Fatal(err)
	}
	if err := tarball.ValidateShape(spool, api.HostedTaskSourceRoleMaxEntries, api.HostedTaskSourceRoleMaxBytes); err != nil {
		t.Fatalf("deployment rejected reviewed archive: %v", err)
	}
	if prefix, err := tarball.RootPrefix(spool); err != nil || prefix != "" {
		t.Fatalf("reviewed archive acquired a source prefix %q: %v", prefix, err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gz.Close() })
	tr := tar.NewReader(gz)
	var restored []api.HostedTaskSourceEntry
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Mode != 0600 && h.Mode != 0700 {
			t.Fatalf("unexpected mode %o in reviewed archive", h.Mode)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		restored = append(restored, api.HostedTaskSourceEntry{
			Path: strings.TrimSuffix(h.Name, "/"), Mode: uint32(h.Mode),
			Directory: h.Typeflag == tar.TypeDir, Content: content,
		})
	}
	if got, err := Digest(restored); err != nil || got != digest {
		t.Fatalf("archive changed reviewed tree: %v", err)
	}
	for i := range entries {
		if entries[i].Path == "worker.js" {
			entries[i].Content = []byte("modified worker")
		}
	}
	if out, err := PackReviewedArchive(entries, digest); !errors.Is(err, ErrInvalid) || out != nil {
		t.Fatalf("archive accepted a changed tree: %v", err)
	}
}
