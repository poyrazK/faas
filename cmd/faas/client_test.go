package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// uuidV4Shape is the canonical RFC 4122 v4 shape: 36 chars, hex with
// dashes at 8/13/18/23, '4' at position 14, [89ab] at position 19.
var uuidV4Shape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestClient_MutatingCallsCarryIdempotencyKey (issue #64 D3) pins the
// invariant: every non-GET client method forwards a UUID-v4-shaped
// Idempotency-Key header to apid. The server middleware at
// cmd/apid/server.go dedupes on this header (24h replay) so a retried
// CLI call returns the original envelope rather than double-charging
// or double-creating.
//
// If a future method is added without Idempotency-Key wiring, this
// test fails — that's the intentional tripwire.
func TestClient_MutatingCallsCarryIdempotencyKey(t *testing.T) {
	cases := []struct {
		name string
		call func(c *Client) error
	}{
		// Mutating methods routed through Client.do.
		{"CreateApp", func(c *Client) error {
			_, err := c.CreateApp(context.Background(), api.CreateAppRequest{Slug: "x"})
			return err
		}},
		{"UpdateApp", func(c *Client) error {
			_, err := c.UpdateApp(context.Background(), "x", api.UpdateAppRequest{})
			return err
		}},
		{"DeleteApp", func(c *Client) error { return c.DeleteApp(context.Background(), "x") }},
		{"RenameApp", func(c *Client) error { _, err := c.RenameApp(context.Background(), "x", "y"); return err }},
		{"Rollback", func(c *Client) error { _, err := c.Rollback(context.Background(), "x"); return err }},
		{"Park", func(c *Client) error { return c.Park(context.Background(), "x") }},
		{"Wake", func(c *Client) error { return c.Wake(context.Background(), "x") }},
		{"RestoreAccount", func(c *Client) error { _, err := c.RestoreAccount(context.Background()); return err }},
		{"ChangePlan", func(c *Client) error { _, err := c.ChangePlan(context.Background(), "hobby"); return err }},
		{"CreateDomain", func(c *Client) error {
			_, err := c.CreateDomain(context.Background(), api.CreateCustomDomainRequest{Domain: "x", AppID: "y"})
			return err
		}},
		{"DeleteDomain", func(c *Client) error { return c.DeleteDomain(context.Background(), "x") }},
		{"CreateCron", func(c *Client) error {
			_, err := c.CreateCron(context.Background(), "y", api.CreateCronRequest{AppID: "y", Schedule: "* * * * *"})
			return err
		}},
		{"DeleteCron", func(c *Client) error { return c.DeleteCron(context.Background(), "1") }},
		{"CreateKey", func(c *Client) error { _, err := c.CreateKey(context.Background(), "lbl"); return err }},
		{"DeleteKey", func(c *Client) error { return c.DeleteKey(context.Background(), "1") }},
		{"SetSecret", func(c *Client) error { return c.SetSecret(context.Background(), "x", "K", "v") }},
		{"UnsetSecret", func(c *Client) error { return c.UnsetSecret(context.Background(), "x", "K") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Idempotency-Key")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte("{}"))
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "fp_test")
			_ = tc.call(c)
			if got == "" {
				t.Fatal("missing Idempotency-Key on mutating call")
			}
			if !uuidV4Shape.MatchString(got) {
				t.Errorf("Idempotency-Key %q is not UUID v4 shape", got)
			}
		})
	}
}

// TestClient_GETCallsDoNotCarryIdempotencyKey is the read-side counterpart:
// GETs must NOT send the header. apid's idempotency middleware only
// stores on POST/PATCH/DELETE; sending the header on a GET is harmless
// but wasteful and pollutes cache keys.
func TestClient_GETCallsDoNotCarryIdempotencyKey(t *testing.T) {
	cases := []struct {
		name string
		call func(c *Client) error
	}{
		{"Whoami", func(c *Client) error { _, err := c.Whoami(context.Background()); return err }},
		{"ListApps", func(c *Client) error { _, err := c.ListApps(context.Background()); return err }},
		{"GetApp", func(c *Client) error { _, err := c.GetApp(context.Background(), "x"); return err }},
		{"ListInstances", func(c *Client) error { _, err := c.ListInstances(context.Background(), "x"); return err }},
		{"ListDomains", func(c *Client) error { _, err := c.ListDomains(context.Background()); return err }},
		{"ListCrons", func(c *Client) error { _, err := c.ListCrons(context.Background(), "x"); return err }},
		{"ListKeys", func(c *Client) error { _, err := c.ListKeys(context.Background()); return err }},
		{"ListSecrets", func(c *Client) error { _, err := c.ListSecrets(context.Background(), "x"); return err }},
		{"GetUsage", func(c *Client) error { _, err := c.GetUsage(context.Background(), ""); return err }},
		{"GetStatusSLO", func(c *Client) error { _, err := c.GetStatusSLO(context.Background()); return err }},
		{"GetDeployment", func(c *Client) error { _, err := c.GetDeployment(context.Background(), "d1"); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Idempotency-Key")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{}"))
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "fp_test")
			_ = tc.call(c)
			if got != "" {
				t.Errorf("GET leaked Idempotency-Key: %q", got)
			}
		})
	}
}

// TestClient_DeleteAccount_ExplicitKeyWins locks the
// commands4.go::cmdAccountDelete contract: the explicit `cli-delete-…`
// key (used for traceability / audit) is preserved verbatim rather
// than replaced by the auto-mint.
func TestClient_DeleteAccount_ExplicitKeyWins(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.DeleteAccount(context.Background(), "cli-delete-deadbeef"); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if got != "cli-delete-deadbeef" {
		t.Errorf("explicit key not preserved: got %q, want %q", got, "cli-delete-deadbeef")
	}
}

// TestClient_DeleteAccount_AutoMintsWhenKeyEmpty is the issue #64 D3
// counterpart: if the caller doesn't supply a key, the client must
// still set one (server middleware only acts when the header is
// present).
func TestClient_DeleteAccount_AutoMintsWhenKeyEmpty(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")
	if _, err := c.DeleteAccount(context.Background(), ""); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if got == "" {
		t.Fatal("missing Idempotency-Key on DeleteAccount (empty-key path)")
	}
	if !uuidV4Shape.MatchString(got) {
		t.Errorf("Idempotency-Key %q is not UUID v4 shape", got)
	}
}

// TestClient_DeployTarball_AutoMintsIdempotencyKey pins the same
// invariant for the hand-rolled DeployTarball path (it bypasses Client.do).
func TestClient_DeployTarball_AutoMintsIdempotencyKey(t *testing.T) {
	// Write a tiny valid tarball-like file (the handler doesn't validate
	// the body — apid does — we just need a non-empty source to upload).
	dir := t.TempDir()
	tar := filepath.Join(dir, "src.tar.gz")
	if err := writeMinimalFile(tar); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"d1","status":"pending"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")
	if _, err := DeployTarball(c, context.Background(), "x", tar, "", "", false); err != nil {
		t.Fatalf("DeployTarball: %v", err)
	}
	if got == "" {
		t.Fatal("missing Idempotency-Key on DeployTarball")
	}
	if !uuidV4Shape.MatchString(got) {
		t.Errorf("Idempotency-Key %q is not UUID v4 shape", got)
	}
}

// TestClient_DeployTarball_RejectsSymlink_NoIdempotencyKeySent is the
// load-bearing attack-surface test for `faas deploy --tarball`. A
// customer who runs `ln -s /etc/passwd source.tar.gz && faas deploy
// --tarball source.tar.gz` must NOT have those bytes streamed into the
// multipart source part. The openCustomerFile guard runs before the
// Idempotency-Key mint, so a rejected path produces no UUID, no
// multipart buffer, and no HTTP traffic at all. Skip on Windows
// where os.Symlink is not supported.
func TestClient_DeployTarball_RejectsSymlink_NoIdempotencyKeySent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink not supported on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.tar.gz")
	if err := writeMinimalFile(target); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	link := filepath.Join(dir, "link.tar.gz")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	var requestCount int
	var sawIdempotencyKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Header.Get("Idempotency-Key") != "" {
			sawIdempotencyKey = true
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"d1","status":"pending"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")

	_, err := DeployTarball(c, context.Background(), "x", link, "", "", false)
	if err == nil {
		t.Fatal("DeployTarball accepted a symlinked tarball path")
	}
	if !strings.Contains(err.Error(), "refusing to follow symlink") {
		t.Errorf("error %q does not mention symlink refusal", err)
	}
	if requestCount != 0 {
		t.Errorf("fake apid received %d request(s); want 0 (symlink must be rejected before any HTTP traffic)", requestCount)
	}
	if sawIdempotencyKey {
		t.Error("fake apid saw an Idempotency-Key on a rejected path — guard fired too late")
	}
}

// TestClient_DeployTarball_RejectsDanglingSymlink pins the strongest
// invariant: a symlink whose target does not exist must be rejected by
// the pre-open Lstat BEFORE os.Open runs. If openCustomerFile ever
// regresses to "open first, then check", this test fails — the
// dangling target would cause os.Open to error out, not the guard.
func TestClient_DeployTarball_RejectsDanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink not supported on Windows")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "dangling.tar.gz")
	if err := os.Symlink("/nonexistent/never-existed", link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"d1","status":"pending"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")

	_, err := DeployTarball(c, context.Background(), "x", link, "", "", false)
	if err == nil {
		t.Fatal("DeployTarball accepted a dangling symlinked tarball path")
	}
	if !strings.Contains(err.Error(), "refusing to follow symlink") {
		t.Errorf("error %q does not mention symlink refusal", err)
	}
	if requestCount != 0 {
		t.Errorf("fake apid received %d request(s); want 0", requestCount)
	}
}

// TestClient_DeployTarball_RejectsDirectoryAsTarball pins the
// !postInfo.Mode().IsRegular() branch: directories, FIFOs, and device
// nodes are not legitimate tarball inputs even if the customer passed
// them by accident. The post-open check is what catches a directory
// (Go's os.Open happily returns a *File for a directory, so the
// pre-open Lstat alone would not be enough). Runs on Windows too —
// directories are cross-platform.
func TestClient_DeployTarball_RejectsDirectoryAsTarball(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "not-a-tarball")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"d1","status":"pending"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")

	_, err := DeployTarball(c, context.Background(), "x", subdir, "", "", false)
	if err == nil {
		t.Fatal("DeployTarball accepted a directory as the tarball path")
	}
	if !strings.Contains(err.Error(), "non-regular file") {
		t.Errorf("error %q does not mention non-regular file refusal", err)
	}
	if requestCount != 0 {
		t.Errorf("fake apid received %d request(s); want 0", requestCount)
	}
}

// TestClient_DeployTarball_HappyPathStillUploads is the regression
// guard for the openCustomerFile rename: a regular file (the only
// shape the CLI should ever upload) must still produce exactly one
// request, the multipart body must carry the seeded bytes verbatim
// in the "source" part, AND the request must carry a UUID-v4-shaped
// Idempotency-Key (locked here for the success branch — the failure
// branches assert the inverse in TestClient_DeployTarball_Rejects*).
// Together those pin the invariant that openCustomerFile runs BEFORE
// the Idempotency-Key mint: success path mints, failure path does not.
// If the new guard ever silently rejects regular files, this test
// fails — uploads break.
func TestClient_DeployTarball_HappyPathStillUploads(t *testing.T) {
	dir := t.TempDir()
	tar := filepath.Join(dir, "src.tar.gz")
	if err := writeMinimalFile(tar); err != nil {
		t.Fatalf("seed tarball: %v", err)
	}

	var requestCount int
	var sourceBytes []byte
	var gotIdempotencyKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		// Pull the source part out of the multipart body. The CLI
		// always uses field name "source" (client.go:271).
		mr, err := r.MultipartReader()
		if err == nil {
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				if p.FormName() == "source" {
					sourceBytes, _ = io.ReadAll(p)
				}
			}
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"d1","status":"pending"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "fp_test")

	resp, err := DeployTarball(c, context.Background(), "x", tar, "", "", false)
	if err != nil {
		t.Fatalf("DeployTarball: %v", err)
	}
	if resp.ID != "d1" {
		t.Errorf("resp.ID = %q, want %q", resp.ID, "d1")
	}
	if requestCount != 1 {
		t.Errorf("fake apid received %d request(s); want 1", requestCount)
	}
	if string(sourceBytes) != strings.Repeat("x", 64) {
		t.Errorf("source part bytes mismatch: got %q, want 64 'x's", sourceBytes)
	}
	if gotIdempotencyKey == "" {
		t.Error("missing Idempotency-Key on the happy-path upload — guard may now run AFTER the mint")
	}
	if !uuidV4Shape.MatchString(gotIdempotencyKey) {
		t.Errorf("Idempotency-Key %q is not UUID v4 shape", gotIdempotencyKey)
	}
}

// TestClient_NewClientWithDeployTimeout's coverage moved to
// pkg/api/client_test.go once the constructor moved to the public SDK.
// The upload-client-vs-default-client contract is now an SDK invariant;
// the test is rewritten to assert via exported *http.Client accessors.
var _ = NewClientWithDeployTimeout // pin the alias

// TestNewUUIDv4_Shape moved to pkg/api/client_test.go in commit 3
// alongside TestClient_NewClientWithDeployTimeout — both tests poked
// unexported fields/methods on Client that are now in pkg/api.


func writeMinimalFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.WriteString(f, strings.Repeat("x", 64))
	return err
}
