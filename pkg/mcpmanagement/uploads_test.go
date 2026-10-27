// adr: 20261026195952
package mcpmanagement

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type tarEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
	link     string
}

func tarball(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: mode, Size: int64(len(e.body)), Linkname: e.link}
		if e.typeflag != tar.TypeReg {
			h.Size = 0
		}
		if e.typeflag == tar.TypeXGlobalHeader {
			h = &tar.Header{Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "git archive"}}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractUploadKeepsOnlyFilesAndDirectoriesInside(t *testing.T) {
	good := tarball(t,
		tarEntry{typeflag: tar.TypeXGlobalHeader},
		tarEntry{name: "./", typeflag: tar.TypeDir},
		tarEntry{name: "src/", typeflag: tar.TypeDir},
		tarEntry{name: "src/index.js", typeflag: tar.TypeReg, body: "console.log(1)"},
		tarEntry{name: "bin/start", typeflag: tar.TypeReg, mode: 0o755, body: "#!/bin/sh"},
	)
	dir := t.TempDir()
	archive := filepath.Join(t.TempDir(), "good.tar.gz")
	if err := os.WriteFile(archive, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractUpload(archive, dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "src", "index.js")); err != nil || string(data) != "console.log(1)" {
		t.Fatalf("index.js = %q %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "bin", "start")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("bin/start mode = %v %v", info, err)
	}

	for name, archive := range map[string][]byte{
		"traversal":      tarball(t, tarEntry{name: "../escape", typeflag: tar.TypeReg, body: "x"}),
		"nested escape":  tarball(t, tarEntry{name: "src/../../escape", typeflag: tar.TypeReg, body: "x"}),
		"absolute":       tarball(t, tarEntry{name: "/etc/passwd", typeflag: tar.TypeReg, body: "x"}),
		"git config":     tarball(t, tarEntry{name: ".git/config", typeflag: tar.TypeReg, body: "[core]\n\tfsmonitor = touch /tmp/pwned"}),
		"nested gitfile": tarball(t, tarEntry{name: "vendor/.GIT", typeflag: tar.TypeReg, body: "gitdir: /"}),
		"symlink":        tarball(t, tarEntry{name: "link", typeflag: tar.TypeSymlink, link: "/etc"}),
		"hard link":      tarball(t, tarEntry{name: "link", typeflag: tar.TypeLink, link: "/etc/passwd"}),
		"device":         tarball(t, tarEntry{name: "dev", typeflag: tar.TypeChar}),
		"linked project": tarball(t, tarEntry{name: ".gregale/project.json", typeflag: tar.TypeReg, body: "{}"}),
		"duplicate":      tarball(t, tarEntry{name: "a", typeflag: tar.TypeReg, body: "1"}, tarEntry{name: "a", typeflag: tar.TypeReg, body: "2"}),
		"too large":      tarball(t, tarEntry{name: "big", typeflag: tar.TypeReg, body: strings.Repeat("x", 2048)}),
		"not gzip":       []byte("plain text"),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upload.tar.gz")
			if err := os.WriteFile(path, archive, 0o600); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if err := extractUpload(path, target, 1024); err == nil {
				t.Fatal("accepted")
			}
			outside := filepath.Join(filepath.Dir(target), "escape")
			if _, err := os.Stat(outside); err == nil {
				t.Fatal("wrote outside the directory")
			}
		})
	}
}

func TestInlineFilesAreBoundedAndSanitized(t *testing.T) {
	dir := t.TempDir()
	if err := writeInlineFiles(dir, []inlineFile{{Path: "index.js", Content: "ok"}, {Path: "scripts/run", Content: "#!/bin/sh", Executable: true}}); err != nil {
		t.Fatal(err)
	}
	for _, files := range [][]inlineFile{
		nil,
		{{Path: "../x", Content: "x"}},
		{{Path: ".git/hooks/pre-commit", Content: "x"}},
		{{Path: "big", Content: strings.Repeat("x", maxInlineBytes+1)}},
		{{Path: "a", Content: "1"}, {Path: "a", Content: "2"}},
	} {
		if err := writeInlineFiles(t.TempDir(), files); err == nil {
			t.Errorf("accepted %v", files)
		}
	}
}

func TestUploadDeployRunsTheCLIOnASanitizedCopy(t *testing.T) {
	f := newLocalFixture(t, false, "")
	store, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.gregale.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var planned []string
	cli := func(ctx context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
		if args[0] == "deploy" && slices.Contains(args, "--plan") {
			dir := args[slices.Index(args, "--path")+1]
			_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					rel, _ := filepath.Rel(dir, path)
					mu.Lock()
					planned = append(planned, rel)
					mu.Unlock()
				}
				return nil
			})
		}
		return f.cli(ctx, args, stdin)
	}
	cs, ctx := f.connect(t, true, func(o *Options) {
		o.LocalCLI, o.uploads, o.grantID = cli, store, "grant-one"
		o.Environments = &EnvironmentConsent{DefaultWrite: true, Named: map[string]EnvironmentGrant{}}
	})
	tools := listedTools(t, ctx, cs)
	for _, name := range []string{"gregale_create_source_upload", "gregale_plan_upload_deploy", "gregale_apply_upload_deploy"} {
		if !tools[name] {
			t.Fatalf("%s not listed", name)
		}
	}
	if tools["gregale_plan_local_deploy"] {
		t.Fatal("a hosted grant lists local directory deploys")
	}

	created := callTool(t, ctx, cs, "gregale_create_source_upload", map[string]any{"app": "alpha"}, false)
	var uploadURL string
	if err := json.Unmarshal(created["upload_url"], &uploadURL); err != nil || !strings.HasPrefix(uploadURL, "https://api.gregale.test/mcp/uploads/") {
		t.Fatalf("upload = %v", created)
	}
	put := func() *httptest.ResponseRecorder {
		body := tarball(t, tarEntry{name: "server.js", typeflag: tar.TypeReg, body: "require('http')"}, tarEntry{name: "lib/util.js", typeflag: tar.TypeReg, body: "module.exports={}"})
		rec := httptest.NewRecorder()
		store.receive(rec, httptest.NewRequest(http.MethodPut, strings.TrimPrefix(uploadURL, "https://api.gregale.test"), bytes.NewReader(body)))
		return rec
	}
	first := put()
	if first.Code != 201 {
		t.Fatalf("upload = %d %s", first.Code, first.Body.String())
	}
	var received struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &received); err != nil || received.UploadID == "" {
		t.Fatalf("upload receipt %s", first.Body.String())
	}
	if again := put(); again.Code != 404 {
		t.Fatalf("a one-time URL accepted a second upload: %d", again.Code)
	}

	plan := callTool(t, ctx, cs, "gregale_plan_upload_deploy", map[string]any{"app": "alpha", "reason": "first deploy", "upload_id": received.UploadID}, false)
	mu.Lock()
	slices.Sort(planned)
	got := slices.Clone(planned)
	mu.Unlock()
	if !slices.Equal(got, []string{filepath.Join("lib", "util.js"), "server.js"}) {
		t.Fatalf("planned files = %v", got)
	}
	id := planID(t, plan)
	applied := callTool(t, ctx, cs, "gregale_apply_upload_deploy", map[string]any{"plan_id": id}, false)
	if !strings.Contains(string(applied["deployment"]), "release-1") {
		t.Fatalf("apply = %v", applied)
	}
	if calls := f.deployCalls(); len(calls) != 1 || !slices.Contains(calls[0], "mcp-local-"+id) {
		t.Fatalf("deploy calls = %v", calls)
	}
	if reuse := callTool(t, ctx, cs, "gregale_plan_upload_deploy", map[string]any{"app": "alpha", "reason": "again", "upload_id": received.UploadID}, true); toolCode(t, reuse) != "unknown_upload" {
		t.Fatalf("an upload was planned twice: %v", reuse)
	}
	inline := callTool(t, ctx, cs, "gregale_plan_upload_deploy", map[string]any{"app": "alpha", "reason": "inline", "files": []map[string]any{{"path": "server.js", "content": "require('http')"}}}, false)
	if planID(t, inline) == "" {
		t.Fatalf("inline plan = %v", inline)
	}
	if both := callTool(t, ctx, cs, "gregale_plan_upload_deploy", map[string]any{"app": "alpha", "reason": "x", "upload_id": "abc", "files": []map[string]any{{"path": "a", "content": "b"}}}, true); toolCode(t, both) != "invalid_argument" {
		t.Fatalf("both sources accepted: %v", both)
	}
}

func TestUploadsBelongToTheirGrantAndAccount(t *testing.T) {
	store, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.gregale.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	up, _, err := store.create("grant-a", "account-1", "alpha", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.take("grant-a", up.id); err == nil {
		t.Fatal("an upload with no body was planned")
	}
	if _, _, err := store.create("grant-b", "account-1", "beta", 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.create("grant-c", "account-1", "gamma", 1<<20); err == nil {
		t.Fatal("an account held more than two pending uploads")
	}
	if _, _, err := store.create("grant-d", "account-2", "delta", 1<<20); err != nil {
		t.Fatalf("another account was limited: %v", err)
	}
	store.mu.Lock()
	up.received = true
	store.mu.Unlock()
	if _, _, err := store.take("grant-b", up.id); err == nil {
		t.Fatal("another grant planned the upload")
	}
	big, url, err := store.create("grant-d", "account-2", "delta", 8)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	store.receive(rec, httptest.NewRequest(http.MethodPut, strings.TrimPrefix(url, "https://api.gregale.test"), strings.NewReader("more than eight bytes")))
	if rec.Code != http.StatusRequestEntityTooLarge || big.received {
		t.Fatalf("oversized upload = %d", rec.Code)
	}
}
