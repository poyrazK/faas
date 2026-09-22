package gitfetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildArchive assembles a tar.gz body in memory. entries is the
// file-map (relative path → content). rootPrefix is the
// leading "<root>/" prefix the codeload endpoint wraps every
// entry under; pass "" to skip the prefix (the extractor
// detects the no-prefix case).
func buildArchive(t *testing.T, rootPrefix string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		// Apply the root prefix to the entry name. The
		// extractor strips one leading "<root>/" prefix.
		// Build the actual header name verbatim.
		if rootPrefix != "" {
			hdr.Name = filepath.ToSlash(filepath.Join(rootPrefix, name))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := io.WriteString(tw, content); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// withServer spins up an httptest.Server, hands the URL to a
// fetcher pointed at it, and returns the fetcher + cleanup
// closure. The handler is the closure under test. capBytes
// is the streaming cap; maxTotalBytes is the cumulative cap.
// Tests pass 0/0 to disable both (the legacy "no cap" posture
// for the golden fixtures).
func withServer(t *testing.T, handler http.HandlerFunc, capBytes, maxTotalBytes int64) (*httpFetcher, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	hc := &http.Client{Timeout: 5 * time.Second}
	f := NewHTTPWithBase(t.TempDir(), srv.URL, hc, capBytes, maxTotalBytes)
	return f, srv
}

func TestFetch_HappyPath(t *testing.T) {
	body := buildArchive(t, "owner-repo-sha", map[string]string{
		"README.md":          "# Hello\n",
		"src/index.go":       "package main\n",
		"docker-compose.yml": "version: '3'\nservices:\n  api:\n    build: .\n",
	})
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Validate the Authorization header was set.
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing Authorization header: %v", got)
		}
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(body)
	}, 0, 0)
	tree, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "ghs_test")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer func() { _ = tree.Close() }()

	// The root prefix "owner-repo-sha" must be stripped. Files
	// land at FS root.
	fsys := tree.FS()
	for _, p := range []string{"README.md", "src/index.go", "docker-compose.yml"} {
		if _, err := fsys.Open(p); err != nil {
			t.Errorf("missing %q after prefix strip: %v", p, err)
		}
	}
}

func TestFetch_StripPrefixFromAbsoluteEntry(t *testing.T) {
	// After stripping the leading prefix, the entries should
	// land at the FS root even if the entry names contain
	// forward slashes.
	body := buildArchive(t, "owner-sha", map[string]string{
		"a/b/c.txt": "deep",
		"top.txt":   "top",
	})
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}, 0, 0)
	tree, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer func() { _ = tree.Close() }()
	fsys := tree.FS()
	if _, err := fsys.Open("a/b/c.txt"); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
	if _, err := fsys.Open("top.txt"); err != nil {
		t.Errorf("top file missing: %v", err)
	}
}

func TestFetch_Unauthorized(t *testing.T) {
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("token expired"))
	}, 0, 0)
	_, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "expired")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestFetch_NotFound(t *testing.T) {
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}, 0, 0)
	_, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFetch_ArchiveTooLarge(t *testing.T) {
	// Build a body larger than the cap.
	body := buildArchive(t, "owner-sha", map[string]string{
		"big.txt": strings.Repeat("a", 200),
	})
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		// The streaming guard is the load-bearing one; the
		// Content-Length header is also set so the test
		// covers both paths.
		w.Header().Set("Content-Length", "999999")
		_, _ = w.Write(body)
	}, 100, 0)
	_, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Errorf("err = %v, want ErrArchiveTooLarge", err)
	}
}

func TestFetch_BadArchive_GzipFailure(t *testing.T) {
	// Send non-gzip body. The extractor should fail with
	// ErrBadArchive.
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not gzip"))
	}, 0, 0)
	_, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if !errors.Is(err, ErrBadArchive) {
		t.Errorf("err = %v, want ErrBadArchive", err)
	}
}

func TestFetch_RejectsPathEscape(t *testing.T) {
	// Build a tarball with a ".." entry that would escape
	// the archive root.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:     "../escaped.txt",
		Mode:     0o644,
		Size:     4,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte("pwn\n")); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	_ = tw.Close()
	_ = gz.Close()
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}, 0, 0)
	_, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if !errors.Is(err, ErrBadArchive) {
		t.Errorf("err = %v, want ErrBadArchive", err)
	}
}

func TestFetch_RejectsInvalidCommitSHA(t *testing.T) {
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be hit for invalid input")
	}, 0, 0)
	for _, sha := range []string{"", "abc", "ZZZ", "abc 1234567"} {
		_, err := f.Fetch(context.Background(), "owner/repo", sha, "tok")
		if !errors.Is(err, ErrBadArchive) {
			t.Errorf("sha=%q: err = %v, want ErrBadArchive", sha, err)
		}
	}
}

func TestFetch_RejectsInvalidRepoPath(t *testing.T) {
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be hit for invalid input")
	}, 0, 0)
	for _, repo := range []string{"", "no-slash", "owner/repo/extra", "owner repo"} {
		_, err := f.Fetch(context.Background(), repo, "abcdef1234567", "tok")
		if !errors.Is(err, ErrBadArchive) {
			t.Errorf("repo=%q: err = %v, want ErrBadArchive", repo, err)
		}
	}
}

func TestFetch_ContextCancellation(t *testing.T) {
	// Server hangs forever; ctx cancels after 50ms.
	hc := &http.Client{Timeout: 0}
	_ = hc
	f := NewHTTPWithBase(t.TempDir(), "http://127.0.0.1:1", &http.Client{Timeout: 1 * time.Second}, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := f.Fetch(ctx, "owner/repo", "abcdef1234567", "tok")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestTree_CloseIsIdempotent(t *testing.T) {
	body := buildArchive(t, "owner-sha", map[string]string{
		"hi.txt": "hello",
	})
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}, 0, 0)
	tree, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := tree.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := tree.Close(); err != nil {
		t.Errorf("second Close (idempotent): %v", err)
	}
	// After Close, the underlying dir is gone.
	fsys := tree.FS()
	if _, err := fsys.Open("hi.txt"); err == nil {
		t.Error("FS still accessible after Close")
	}
}

func TestTree_FSReturnsValidFilesystem(t *testing.T) {
	// The Tree.FS() should hand back a usable fs.FS — pinned
	// by fstest.MapFS shape comparison so reposcan can
	// consume it directly.
	body := buildArchive(t, "owner-sha", map[string]string{
		"docker-compose.yml": "version: '3'\n",
		"src/main.go":        "package main\n",
	})
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}, 0, 0)
	tree, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer func() { _ = tree.Close() }()

	// Test against the same fs.FS signature reposcan consumes.
	fsys := tree.FS()
	want := fstest.MapFS{
		"docker-compose.yml": &fstest.MapFile{Data: []byte("version: '3'\n")},
		"src/main.go":        &fstest.MapFile{Data: []byte("package main\n")},
	}
	if err := fstest.TestFS(fsys, "docker-compose.yml", "src/main.go"); err != nil {
		t.Errorf("fstest.TestFS: %v", err)
	}
	_ = want // signature shape assert
}

func TestFetch_NoWorkDir(t *testing.T) {
	// WorkDir must already exist before Fetch. The fetcher
	// creates it via MkdirAll, so a fresh tmpdir should
	// work. Verify the temp dir created under it.
	workDir := filepath.Join(t.TempDir(), "fresh")
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buildArchive(t, "r", map[string]string{"x": "y"}))
	}, 0, 0)
	f.workDir = workDir
	tree, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer func() { _ = tree.Close() }()
	if _, err := os.Stat(workDir); err != nil {
		t.Errorf("workDir not created: %v", err)
	}
}

// TestNewHTTPWithLimits_PlanScaleCap_Applied pins the H1
// production cap. NewHTTPWithLimits with PlanScale must
// configure the fetcher to fail any archive above 250 MB
// (compressed). Hobby caps at 100 MB. The legacy NewHTTP
// constructor keeps the zero-cap posture for tests.
func TestNewHTTPWithLimits_PlanScaleCap_Applied(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanScale)
	if limits.SourceTarballMaxMB != 250 {
		t.Fatalf("SourceTarballMaxMB(PlanScale) = %d, want 250", limits.SourceTarballMaxMB)
	}
	f := NewHTTPWithLimits(t.TempDir(), limits)
	wantCompressed := int64(250) * 1024 * 1024
	if f.maxArchiveBytes != wantCompressed {
		t.Errorf("maxArchiveBytes = %d, want %d", f.maxArchiveBytes, wantCompressed)
	}
	wantExpanded := wantCompressed * 5 / 2
	if f.maxTotalBytes != wantExpanded {
		t.Errorf("maxTotalBytes = %d, want %d", f.maxTotalBytes, wantExpanded)
	}
}

func TestNewHTTPWithLimits_HobbyCap_Applied(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	if limits.SourceTarballMaxMB != 100 {
		t.Fatalf("SourceTarballMaxMB(PlanHobby) = %d, want 100", limits.SourceTarballMaxMB)
	}
	f := NewHTTPWithLimits(t.TempDir(), limits)
	if want := int64(100) * 1024 * 1024; f.maxArchiveBytes != want {
		t.Errorf("maxArchiveBytes = %d, want %d", f.maxArchiveBytes, want)
	}
}

// TestFetch_CapEnforcedWhenContentLengthMissing pins M4:
// a hostile provider (or any CDN that elides Content-Length)
// must not bypass the streaming cap. Flush the headers before
// writing to prevent net/http from inferring Content-Length.
func TestFetch_CapEnforcedWhenContentLengthMissing(t *testing.T) {
	body := buildArchive(t, "owner-sha", map[string]string{
		"big.txt": strings.Repeat("a", 2*1024),
	})
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = w.Write(body)
	}, int64(len(body)-10), 0)
	_, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Errorf("err = %v, want ErrArchiveTooLarge", err)
	}
}

// TestFetch_SymlinkLinkname_Rejected pins H3 (defense in
// depth). The type allow-list rejects TypeLink/TypeSymlink
// outright today; this test pins that the Linkname guard
// would catch a "../etc/passwd" target if a future
// maintainer ever relaxes the type allow-list without
// re-reading the security posture.
func TestFetch_SymlinkLinkname_Rejected(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:     "link.txt",
		Linkname: "../../etc/passwd",
		Mode:     0o644,
		Typeflag: tar.TypeSymlink,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	_ = tw.Close()
	_ = gz.Close()
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}, 0, 0)
	_, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if !errors.Is(err, ErrBadArchive) {
		t.Errorf("err = %v, want ErrBadArchive", err)
	}
}

// TestFetch_TruncatedGzip_Rejected pins the gzip trailer
// verification (the post-loop io.Copy(io.Discard, gz)
// drain). Drop the last 8 bytes of a valid gzip body —
// the tar loop reads clean entries and hits io.EOF, but
// the gzip CRC32 + length trailer is corrupt. The extractor
// must surface ErrBadArchive on the post-loop drain.
func TestFetch_TruncatedGzip_Rejected(t *testing.T) {
	body := buildArchive(t, "owner-sha", map[string]string{
		"hi.txt": "hello",
	})
	// A valid gzip trailer is 8 bytes: CRC32 (4) + ISIZE (4).
	// Truncating it forces a CRC mismatch on gz.Close().
	if len(body) < 16 {
		t.Fatalf("test body too short to truncate (%d)", len(body))
	}
	truncated := body[:len(body)-8]
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(truncated)
	}, 0, 0)
	_, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if !errors.Is(err, ErrBadArchive) {
		t.Errorf("err = %v, want ErrBadArchive", err)
	}
}

// TestTree_ConcurrentClose pins H2 (sync.Once race fix).
// 8 goroutines race Close() under the race detector. With
// sync.Once, the underlying os.RemoveAll runs exactly once;
// without it, the test races on `closed bool`.
func TestTree_ConcurrentClose(t *testing.T) {
	body := buildArchive(t, "owner-sha", map[string]string{
		"hi.txt": "hello",
	})
	f, _ := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}, 0, 0)
	tree, err := f.Fetch(context.Background(), "owner/repo", "abcdef1234567", "tok")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	const N = 8
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = tree.Close()
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("goroutine %d Close err = %v", i, e)
		}
	}
	// After all Close() calls, the dir must be gone (a
	// single RemoveAll ran; any further opens fail).
	fsys := tree.FS()
	if _, err := fsys.Open("hi.txt"); err == nil {
		t.Error("FS still accessible after concurrent Close")
	}
}
