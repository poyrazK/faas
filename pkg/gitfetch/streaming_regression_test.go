package gitfetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec §4.2: source-size admission also applies when the provider streams
// without Content-Length, including an archive exactly one byte over the cap.
func TestFetchChunkedArchiveLimit(t *testing.T) {
	body := buildArchive(t, "owner-sha", map[string]string{"main.go": "package main\n"})
	for _, tc := range []struct {
		name string
		cap  int64
		want error
	}{
		{"exact limit", int64(len(body)), nil},
		{"one byte over", int64(len(body) - 1), ErrArchiveTooLarge},
		{"truncated by limit", int64(len(body) / 2), ErrArchiveTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetcher, _ := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush() // Force chunked framing, without Content-Length.
				_, _ = w.Write(body)
			}, tc.cap, 0)
			tree, err := fetcher.Fetch(context.Background(), "owner/repo", "abcdef1234567", "test-token")
			if tree != nil {
				defer func() { _ = tree.Close() }()
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Fetch error = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				entries, readErr := os.ReadDir(fetcher.workDir)
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("failed fetch left extracted source: %v, %v", entries, readErr)
				}
			}
		})
	}
}

// GitHub staging repackages this FS with tar.FileInfoHeader; stripping execute
// bits here makes checked-in build/entrypoint scripts unusable downstream.
func TestFetchPreservesExecutableScripts(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode int64
		want fs.FileMode
	}{
		{"script", 0o755, 0o755},
		{"ordinary file", 0o644, 0o644},
		{"untrusted special bits", 0o6777, 0o755},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			gz := gzip.NewWriter(&body)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: "owner-sha/run.sh", Mode: tc.mode, Size: 2, Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte("ok")); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			fetcher, _ := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(body.Bytes())
			}, 0, 0)
			tree, err := fetcher.Fetch(context.Background(), "owner/repo", "abcdef1234567", "test-token")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tree.Close() }()
			info, err := fs.Stat(tree.FS(), "run.sh")
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode() != tc.want {
				t.Fatalf("mode = %v, want %v", info.Mode(), tc.want)
			}
		})
	}
}

func TestFetchAcceptsGitHubPAXMetadata(t *testing.T) {
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	for _, hdr := range []*tar.Header{
		{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "abcdef1234567"}},
		{Name: "owner-sha/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "owner-sha/main.go", Typeflag: tar.TypeReg, Mode: 0o644},
	} {
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	fetcher, _ := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body.Bytes())
	}, 0, 0)
	tree, err := fetcher.Fetch(context.Background(), "owner/repo", "abcdef1234567", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tree.Close() }()
	if _, err := fs.Stat(tree.FS(), "main.go"); err != nil {
		t.Fatalf("PAX metadata prevented stripping the repository wrapper: %v", err)
	}
	if _, err := fs.Stat(tree.FS(), "pax_global_header"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("metadata was extracted as a source file: %v", err)
	}
}

func TestWriteOneFileChecksExpandedBudgetBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want error
	}{
		{"exact remaining budget", "x", nil},
		{"entry exceeds remaining budget", "xx", ErrArchiveTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := strings.NewReader(tc.body)
			target := filepath.Join(t.TempDir(), "source.txt")
			total := int64(3)
			err := writeOneFile(target, reader, int64(len(tc.body)), 1024, &total, 4, 0o644)
			if !errors.Is(err, tc.want) {
				t.Fatalf("write error = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				if reader.Len() != len(tc.body) || total != 3 {
					t.Errorf("oversized entry consumed bytes before admission: unread=%d total=%d", reader.Len(), total)
				}
				if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("oversized entry was created on disk: %v", err)
				}
			} else if total != 4 {
				t.Fatalf("total = %d, want 4", total)
			}
		})
	}
}
