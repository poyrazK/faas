// cmd/gregale/cmd_deploy_zero_config_test.go — integration coverage
// for the refactored `gregale deploy` zero-config path (issue #1182).
//
// These tests exercise the full pipeline end-to-end through
// cmdDeployTarball → resolveZeroConfigProvenance → gitArchiveHEAD
// → buildCreateRequest → createOrFetchApp → DeployTarball, against
// a stub apid. The git repo is a real `git init`d tempdir with a
// GitHub remote (so the zero-config branch fires), and the cwd is
// chdir'd into it for the duration of each test (chdir is
// restored by t.Cleanup).

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// initZeroConfigRepo creates a tempdir git repo with a GitHub
// `origin` remote and a single commit. Returns the tempdir path.
// Mirrors initTestRepo (git_local_test.go:281) but with the
// remote pre-wired so the zero-config branch recognises the
// repo as deployable without the operator running `git remote add`.
// The repo carries a package.json so resolveDeployShape picks the
// app shape (the cwd-auto-pack branch the zero-config path falls
// through to when no --function / --app override is passed).
func initZeroConfigRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	for name, body := range map[string]string{
		"README.md":    "hello\n",
		"package.json": "{}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	for _, args := range [][]string{
		{"add", "README.md"},
		{"add", "package.json"},
		{"commit", "-q", "-m", "initial commit"},
		{"remote", "add", "origin", "git@github.com:acme/demo.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}

// withCwd chdirs into dir for the duration of the test and
// restores the original cwd on cleanup. Wraps the chdir pattern
// scattered across cli_test.go / git_local_test.go /
// cmd_deploy_annotations_test.go into a single helper so the
// new tests are uniform.
func withCwd(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

// zeroConfigStubServer captures the route shape and returns the
// canned responses cmdDeployTarball's zero-config branch needs.
// The handler dispatcher is wired per-test so each case can
// customise its 409 vs 200 paths; the helper just owns the
// boilerplate routes (Whoami, SSE logs stream) that every test
// uses.
type zeroConfigStubServer struct {
	t        *testing.T
	srv      *httptest.Server
	gotCalls map[string]int
}

func newZeroConfigStubServer(t *testing.T, custom func(http.ResponseWriter, *http.Request, *zeroConfigStubServer)) *zeroConfigStubServer {
	z := &zeroConfigStubServer{t: t, gotCalls: map[string]int{}}
	z.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Default routes common to every test.
		switch {
		case r.URL.Path == "/v1/account" && r.Method == "GET":
			z.gotCalls["whoami"]++
			_ = json.NewEncoder(w).Encode(api.AccountResponse{
				ID: "acct-1", Email: "ops@acme.test", Plan: "free",
				Status: "active",
			})
			return
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
			return
		}
		// Test-specific routes (CreateApp, GetApp, DeployTarball).
		if custom != nil {
			custom(w, r, z)
			return
		}
		http.Error(w, "no", 404)
	}))
	t.Cleanup(z.srv.Close)
	return z
}

// TestDeployZeroConfig_HappyPath_NewApp pins the headline AC:
// a fresh repo with a GitHub origin, no flags, hits the normal
// buildCreateRequest → CreateApp → DeployTarball pipeline. The
// stub server records the order of /v1/apps and /v1/apps/{slug}/
// deployments so the test catches a regression to the old
// `cmdDeployZeroConfig` path that bypassed CreateApp.
func TestDeployZeroConfig_HappyPath_NewApp(t *testing.T) {
	repo := initZeroConfigRepo(t)
	withCwd(t, repo)

	stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, z *zeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == "POST":
			z.gotCalls["create"]++
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == "POST":
			z.gotCalls["deploy"]++
			// Drain the multipart body so the connection reuses cleanly.
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "demo"})
		default:
			http.Error(w, "no", 404)
		}
	})
	t.Setenv("FAAS_API", stub.srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeployTarball([]string{"--name", "demo"}); code != 0 {
		t.Fatalf("zero-config deploy exit = %d, want 0", code)
	}
	if stub.gotCalls["create"] == 0 {
		t.Errorf("CreateApp should be called for a new slug; was the zero-config path short-circuited again?")
	}
	if stub.gotCalls["deploy"] == 0 {
		t.Errorf("DeployTarball should be called after CreateApp")
	}
	// Sanity: Whoami fired too (per-plan cap round-trip).
	if stub.gotCalls["whoami"] == 0 {
		t.Errorf("Whoami round-trip for per-plan cap should have fired")
	}
}

// TestDeployZeroConfig_409SameAccount_GETProbeAndPATCH pins the
// hybrid slug-conflict probe: CreateApp 409 + GetApp 200 →
// UpdateApp PATCH mirrors --require-authn (existing #560 contract).
// This is the in-account re-deploy path — the customer already
// owns the slug and is just shipping a new commit.
func TestDeployZeroConfig_409SameAccount_GETProbeAndPATCH(t *testing.T) {
	repo := initZeroConfigRepo(t)
	withCwd(t, repo)

	stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, z *zeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == "POST":
			z.gotCalls["create"]++
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Problem{
				Status: 409, Code: api.CodeConflict, Title: "Conflict", Detail: "app exists",
			})
		case r.URL.Path == "/v1/apps/demo" && r.Method == "GET":
			z.gotCalls["getapp"]++
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.URL.Path == "/v1/apps/demo" && r.Method == "PATCH":
			z.gotCalls["patch"]++
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo", RequireAuthn: true})
		case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == "POST":
			z.gotCalls["deploy"]++
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "demo"})
		default:
			http.Error(w, "no", 404)
		}
	})
	t.Setenv("FAAS_API", stub.srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// --require-authn is the flag that triggers the PATCH on
	// the existing-app branch (preserve #560 UX).
	if code := cmdDeployTarball([]string{"--require-authn", "--name", "demo"}); code != 0 {
		t.Fatalf("zero-config deploy 409-same-account exit = %d, want 0", code)
	}
	if stub.gotCalls["getapp"] == 0 {
		t.Errorf("GetApp probe should fire after CreateApp 409 (hybrid slug-conflict disambiguation)")
	}
	if stub.gotCalls["patch"] == 0 {
		t.Errorf("UpdateApp PATCH should mirror --require-authn on the existing-app branch")
	}
	if stub.gotCalls["deploy"] == 0 {
		t.Errorf("DeployTarball should still run after the in-account PATCH")
	}
}

// TestDeployZeroConfig_409OtherAccount_HardFail pins the new
// safety boundary added in issue #1182: when CreateApp 409s and
// GetApp returns 404 (apid's silent IDOR 404 — the slug is owned
// by another account, or the row vanished in a race), the CLI
// hard-fails with a clear "slug already in use" message and does
// NOT upload the tarball.
func TestDeployZeroConfig_409OtherAccount_HardFail(t *testing.T) {
	repo := initZeroConfigRepo(t)
	withCwd(t, repo)

	stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, z *zeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == "POST":
			z.gotCalls["create"]++
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Problem{
				Status: 409, Code: api.CodeConflict, Title: "Conflict", Detail: "slug taken",
			})
		case r.URL.Path == "/v1/apps/demo" && r.Method == "GET":
			z.gotCalls["getapp"]++
			http.Error(w, "no such app", http.StatusNotFound)
		case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == "POST":
			z.gotCalls["deploy"]++
			t.Errorf("DeployTarball should NOT fire when GetApp probe 404s (other-account slug)")
		default:
			http.Error(w, "no", 404)
		}
	})
	t.Setenv("FAAS_API", stub.srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeployTarball([]string{"--name", "demo"}); code == 0 {
		t.Fatalf("zero-config deploy 409-other-account should hard-fail, got code 0")
	}
	if stub.gotCalls["getapp"] == 0 {
		t.Errorf("GetApp probe should fire after CreateApp 409 to disambiguate")
	}
	if stub.gotCalls["deploy"] != 0 {
		t.Errorf("DeployTarball fired even though slug belongs to another account")
	}
}

// TestDeployZeroConfig_JSONShape pins that the refactored path
// honours --json: stdout is a single JSON document describing the
// deployment, not the human "packing / build queued" lines.
// The legacy zero-config path always streamed logs regardless
// of --json (issue #1182 §3.2).
func TestDeployZeroConfig_JSONShape(t *testing.T) {
	repo := initZeroConfigRepo(t)
	withCwd(t, repo)

	stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, z *zeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == "POST":
			z.gotCalls["create"]++
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == "POST":
			z.gotCalls["deploy"]++
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "demo"})
		default:
			http.Error(w, "no", 404)
		}
	})
	t.Setenv("FAAS_API", stub.srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// Capture stdout to verify --json shape. The CLI renders --json
	// via writeJSON, which writes to the package-level osStdout
	// (commands3.go:45). Override it for the test.
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })

	code := cmdDeployTarball([]string{"--json", "--name", "demo"})
	out := stdout.Bytes()

	if code != 0 {
		t.Fatalf("zero-config deploy --json exit = %d, want 0", code)
	}
	// Sanity: stdout must be a valid JSON document, not the human
	// "packing / build queued" lines. The deployment ID is the
	// load-bearing field — its presence confirms --json went
	// through DeployTarball's JSON render branch, not streamDeployLogs.
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("--json stdout should be a single JSON document, got parse error: %v\nstdout: %q", err, out)
	}
	if id, ok := doc["id"].(string); !ok || id != "d1" {
		t.Errorf("--json stdout missing deployment id; got: %v", doc)
	}
	// Negative assertion: no human "build queued" / "Deployed." line.
	if strings.Contains(string(out), "build queued") || strings.Contains(string(out), "Deployed.") {
		t.Errorf("--json stdout leaked human deploy log: %s", out)
	}
}

// TestDeployZeroConfig_ReceiptContainsProvenance pins issue #1182
// §P1 follow-up: the `--json` wire on the zero-config path emits
// a DeployReceipt that carries the customer's HEAD SHA, dirty
// flag, customer-facing app_url, and the SHA-256 of the tarball
// bytes just shipped. The bare DeploymentResponse-unmarshal pin
// (TestDeployZeroConfig_JSONShape above) still passes because the
// receipt's extra top-level keys are silently dropped — this test
// pins the receipt fields too so a future regression that drops
// the wire-up at commands2.go:1638 is caught here.
func TestDeployZeroConfig_ReceiptContainsProvenance(t *testing.T) {
	repo := initZeroConfigRepo(t)
	withCwd(t, repo)

	// Resolve the expected HEAD SHA + a known source-bytes sha
	// from the filtered git archive of the test repo. Computing the
	// expected source_sha256 from the file the test just wrote
	// would couple the pin to the test's own bookkeeping; running
	// `git archive` plus the production filter mirrors what
	// cmdDeployTarball does, so the expected digest is the value
	// the receipt must report.
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = repo
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	expectedSHA := strings.TrimRight(string(shaOut), "\n")

	archivePath := filepath.Join(t.TempDir(), "expected.tar.gz")
	archiveCmd := exec.Command("git", "archive", "HEAD", "--format=tar.gz", "-o", archivePath)
	archiveCmd.Dir = repo
	if out, err := archiveCmd.CombinedOutput(); err != nil {
		t.Fatalf("git archive: %v\n%s", err, out)
	}
	filteredPath, _, _, err := packGitArchive(archivePath, defaultZeroConfigSourceCapMB, modeOff)
	if err != nil {
		t.Fatalf("filter expected tarball: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(filteredPath) })
	expectedFileSHA, err := fileSHA256Hex(t, filteredPath)
	if err != nil {
		t.Fatalf("sha256(expected tarball): %v", err)
	}

	stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, z *zeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == "POST":
			z.gotCalls["create"]++
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
		case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == "POST":
			z.gotCalls["deploy"]++
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "demo"})
		default:
			http.Error(w, "no", 404)
		}
	})
	t.Setenv("FAAS_API", stub.srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })

	code := cmdDeployTarball([]string{"--json", "--name", "demo"})
	if code != 0 {
		t.Fatalf("zero-config deploy --json exit = %d, want 0\nstdout: %s", code, stdout.String())
	}

	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("--json stdout parse: %v\nstdout: %s", err, stdout.String())
	}
	if got, ok := doc["id"].(string); !ok || got != "d1" {
		t.Errorf("id = %v (present=%v), want d1", doc["id"], ok)
	}
	if got, ok := doc["app_url"].(string); !ok || got != "https://demo.gregale.dev" {
		t.Errorf("app_url = %v (present=%v), want https://demo.gregale.dev", doc["app_url"], ok)
	}
	if got, ok := doc["commit_sha"].(string); !ok || got != expectedSHA {
		t.Errorf("commit_sha = %v (present=%v), want %s", doc["commit_sha"], ok, expectedSHA)
	}
	// dirty uses json:",omitempty" — on a clean repo (Dirty=false)
	// the key is dropped from the wire. The dirty=true variant
	// is exercised by TestDeployZeroConfig_DirtyTree_OnlyCommitsShipped
	// below; here we pin only that the wire is absence-clean for
	// a clean tree (no spurious true).
	if _, present := doc["dirty"]; present {
		t.Errorf("dirty must be absent on clean repo (omitempty); got %v", doc["dirty"])
	}
	if got, ok := doc["source_sha256"].(string); !ok || got != expectedFileSHA {
		t.Errorf("source_sha256 = %v (present=%v), want %s", doc["source_sha256"], ok, expectedFileSHA)
	}
}

// fileSHA256Hex returns the lower-case hex sha256 of the file at
// path. Mirrors tarballSHA256 in git_local.go but is duplicated
// here so the test can pin the expected digest without exporting
// the production helper (which operates on the CLI's own temp
// files and is a CLI-only side-effect).
func fileSHA256Hex(t *testing.T, path string) (string, error) {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test helper; reads the file the test just wrote
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TestDeployZeroConfig_DirtyTree_OnlyCommitsShipped pins the
// headline guarantee of the refactored zero-config path: when
// the working tree has uncommitted + untracked changes, the
// deployed tarball contains ONLY the committed HEAD tree (issue
// #1182 §3.3). Pre-fix, the cwd-auto-pack switch unconditionally
// re-packed the working tree and overwrote the git-archive
// tarball, so the dirty-warning was a lie and untracked files
// shipped to the build. The fix wraps the switch in
// `if *tarball == ""` so git-archive's tarball is the only thing
// the multipart upload sends.
func TestDeployZeroConfig_DirtyTree_OnlyCommitsShipped(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	// Commit a tracked file with a known committed body.
	committed := filepath.Join(repo, "COMMITTED.md")
	if err := os.WriteFile(committed, []byte("committed content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile COMMITTED.md: %v", err)
	}
	for _, args := range [][]string{
		{"add", "COMMITTED.md"},
		{"commit", "-q", "-m", "initial commit"},
		{"remote", "add", "origin", "git@github.com:acme/dirty.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	// Now mutate the working tree:
	//   1. untracked file UNTRACKED.txt (must NOT ship)
	//   2. modify COMMITTED.md so it differs from HEAD (must NOT ship)
	if err := os.WriteFile(filepath.Join(repo, "UNTRACKED.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("WriteFile UNTRACKED.txt: %v", err)
	}
	if err := os.WriteFile(committed, []byte("modified working-tree content\n"), 0o644); err != nil {
		t.Fatalf("overwrite COMMITTED.md: %v", err)
	}
	withCwd(t, repo)

	// Stub server captures the multipart `source` part so the test
	// can untar it and assert exactly which files shipped.
	var sourceBytes []byte
	stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, z *zeroConfigStubServer) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == "POST":
			z.gotCalls["create"]++
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "dirty"})
		case r.URL.Path == "/v1/apps/dirty/deployments" && r.Method == "POST":
			z.gotCalls["deploy"]++
			mr, err := r.MultipartReader()
			if err != nil {
				t.Errorf("multipart read: %v", err)
				return
			}
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				if p.FormName() == "source" {
					sourceBytes, _ = io.ReadAll(p)
				}
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "dirty"})
		default:
			http.Error(w, "no", 404)
		}
	})
	t.Setenv("FAAS_API", stub.srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeployTarball([]string{"--name", "dirty"}); code != 0 {
		t.Fatalf("zero-config dirty-tree deploy exit = %d, want 0", code)
	}
	if len(sourceBytes) == 0 {
		t.Fatalf("DeployTarball received empty `source` part")
	}

	// Untar the captured source bytes and assert the file set is
	// EXACTLY the committed tree: COMMITTED.md with the committed
	// body, and no UNTRACKED.txt. The cwd-auto-pack bug shipped
	// UNTRACKED.txt and the modified COMMITTED.md body; this
	// test fails on that regression.
	gz, err := gzip.NewReader(bytes.NewReader(sourceBytes))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	sawCommitted := false
	sawUntracked := false
	var committedBody []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		switch filepath.Base(h.Name) {
		case "COMMITTED.md":
			sawCommitted = true
			committedBody, _ = io.ReadAll(tr)
		case "UNTRACKED.txt":
			sawUntracked = true
		}
	}
	if !sawCommitted {
		t.Errorf("expected COMMITTED.md in deployed tarball (HEAD-only); got set without it")
	}
	if sawUntracked {
		t.Errorf("UNTRACKED.txt leaked into the deployed tarball; the cwd-auto-pack branch re-packed the working tree over the git-archive tarball")
	}
	if !bytes.Contains(committedBody, []byte("committed content")) {
		t.Errorf("deployed COMMITTED.md body is not the HEAD commit; the working-tree overwrite leaked: %q", committedBody)
	}
	if bytes.Contains(committedBody, []byte("modified working-tree content")) {
		t.Errorf("deployed COMMITTED.md body contains the working-tree modification; HEAD-only contract broken")
	}
}

// Compile-time guard that ctx is imported even when --json path
// doesn't use it (the dirty-tree test uses context.Background
// implicitly via the SDK call).
var _ = context.Background
