package cirunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func archiveBytes(t *testing.T, headers []tar.Header, bodies []string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	writer := tar.NewWriter(gz)
	for i, header := range headers {
		if header.Typeflag == tar.TypeReg {
			header.Size = int64(len(bodies[i]))
		}
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Size != 0 {
			if _, err := io.WriteString(writer, bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestArchiveExtraction(t *testing.T) {
	cases := []struct {
		name    string
		headers []tar.Header
		bodies  []string
		valid   bool
	}{
		{name: "normal", headers: []tar.Header{{Name: "repo/", Typeflag: tar.TypeDir}, {Name: "repo/src/test.txt", Typeflag: tar.TypeReg, Mode: 0o6755}}, bodies: []string{"", "content"}, valid: true},
		{name: "absolute", headers: []tar.Header{{Name: "/repo/source", Typeflag: tar.TypeReg}}, bodies: []string{"x"}},
		{name: "traversal", headers: []tar.Header{{Name: "repo/../../escape", Typeflag: tar.TypeReg}}, bodies: []string{"x"}},
		{name: "dot_segment", headers: []tar.Header{{Name: "repo/a/./x", Typeflag: tar.TypeReg}}, bodies: []string{"x"}},
		{name: "git_metadata", headers: []tar.Header{{Name: "repo/.git/config", Typeflag: tar.TypeReg}}, bodies: []string{"x"}},
		{name: "symlink", headers: []tar.Header{{Name: "repo/link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}}, bodies: []string{""}},
		{name: "hardlink", headers: []tar.Header{{Name: "repo/link", Linkname: "repo/other", Typeflag: tar.TypeLink}}, bodies: []string{""}},
		{name: "device", headers: []tar.Header{{Name: "repo/device", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3}}, bodies: []string{""}},
		{name: "multiple_roots", headers: []tar.Header{{Name: "repo/file", Typeflag: tar.TypeReg}, {Name: "other/file", Typeflag: tar.TypeReg}}, bodies: []string{"x", "y"}},
		{name: "duplicate", headers: []tar.Header{{Name: "repo/file", Typeflag: tar.TypeReg}, {Name: "repo/file", Typeflag: tar.TypeReg}}, bodies: []string{"x", "y"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			err = extract(t.Context(), bytes.NewReader(archiveBytes(t, tc.headers, tc.bodies)), root)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				raw, err := root.ReadFile("src/test.txt")
				if err != nil || string(raw) != "content" {
					t.Fatal("source was not extracted")
				}
				info, _ := root.Stat("src/test.txt")
				if info.Mode()&os.ModeSetuid != 0 || info.Mode()&os.ModeSetgid != 0 {
					t.Fatal("archive privilege bits survived")
				}
			} else if err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	t.Run("expanded_bytes", func(t *testing.T) {
		reader := &expandedReader{ctx: t.Context(), reader: strings.NewReader("too large"), left: 2}
		if _, err := io.ReadAll(reader); err == nil {
			t.Fatal("expanded byte bound was ignored")
		}
	})
	t.Run("trailing_member", func(t *testing.T) {
		raw := archiveBytes(t, []tar.Header{{Name: "repo/file", Typeflag: tar.TypeReg}}, []string{"safe"})
		raw = append(raw, raw...)
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()
		if extract(t.Context(), bytes.NewReader(raw), root) == nil {
			t.Fatal("multiple gzip members accepted")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()
		if extract(ctx, bytes.NewReader(archiveBytes(t, []tar.Header{{Name: "repo/file", Typeflag: tar.TypeReg}}, []string{"x"})), root) == nil {
			t.Fatal("cancelled extraction continued")
		}
	})
}

func TestGuestTransportRejectsRedirects(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { leaked.Add(1); w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	proof := ci.RuntimeProof{RunID: uuid.NewString(), InstanceID: uuid.NewString(), Capability: strings.Repeat("a", 64)}
	transport, err := NewTransport(origin.URL, proof, origin.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Control(t.Context()); err == nil || leaked.Load() != 0 {
		t.Fatal("redirect leaked a runtime capability")
	}
	for _, url := range []string{"http://example.invalid", "https://token@example.invalid", "https://example.invalid/?token=x", "https://example.invalid/path"} {
		if _, err := NewTransport(url, proof, nil); err == nil {
			t.Fatalf("unsafe runtime origin accepted: %s", url)
		}
	}
}

func TestGuestManifestAndHostGuard(t *testing.T) {
	account, execution, attempt := uuid.NewString(), uuid.NewString(), uuid.NewString()
	prefix := "ci/accounts/" + account + "/executions/" + execution + "/attempts/" + attempt + "/"
	manifest := ci.JobsManifest{Version: 1, AccountID: account, ExecutionID: execution, AttemptID: attempt, Repository: "gregale/ci", CommitSHA: strings.Repeat("a", 40), Deadline: time.Now().Add(time.Minute),
		Job:    ci.PlannedJob{Name: "test", Job: ci.Job{Image: "registry.example/test@sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 60, Steps: []ci.Step{{Name: "unit", Run: "never executed by this test"}}}},
		Source: ci.SourceArchive{Key: prefix + "source/" + strings.Repeat("a", 64) + ".tar.gz", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 1}, OutputPrefix: prefix + "output/"}
	raw, _ := json.Marshal(manifest)
	encoded := base64.StdEncoding.EncodeToString(raw)
	if _, err := ci.DecodeJobsManifest(encoded); err != nil {
		t.Fatal(err)
	}
	manifest.Source.Key = "other/account/source.tar.gz"
	raw, _ = json.Marshal(manifest)
	if _, err := ci.DecodeJobsManifest(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("foreign source accepted")
	}
	if _, err := ci.DecodeJobsManifest(strings.Repeat("a", base64.StdEncoding.EncodedLen(api.CIJobsManifestMaxBytes)+1)); err == nil {
		t.Fatal("unbounded manifest accepted")
	}
	log := &prefixLog{}
	_, _ = log.Write(bytes.Repeat([]byte("x"), api.CILogMaxBytesPerAttempt+1))
	kept, truncated := log.snapshot()
	if !truncated || len(kept) != api.CILogMaxBytesPerAttempt {
		t.Fatal("log prefix bound lost")
	}
}
