package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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
	if _, err := c.DeployTarball(context.Background(), "x", tar, "", "", false); err != nil {
		t.Fatalf("DeployTarball: %v", err)
	}
	if got == "" {
		t.Fatal("missing Idempotency-Key on DeployTarball")
	}
	if !uuidV4Shape.MatchString(got) {
		t.Errorf("Idempotency-Key %q is not UUID v4 shape", got)
	}
}

// TestClient_NewClientWithDeployTimeout honors the longer upload
// timeout (issue #64 D4). We don't simulate a slow upload here — the
// test pins the constructor contract so a future refactor doesn't
// drop the field. The 30s default still applies when no override is
// set.
func TestClient_NewClientWithDeployTimeout(t *testing.T) {
	t.Run("zero timeout falls back to default", func(t *testing.T) {
		c := NewClientWithDeployTimeout("http://x", "", 0)
		if c.uploadHTTP() != c.http {
			t.Error("zero timeout should fall back to default http client")
		}
	})
	t.Run("positive timeout gets its own client", func(t *testing.T) {
		c := NewClientWithDeployTimeout("http://x", "", 5*60_000_000_000) // 5 min
		if c.uploadHTTP() == c.http {
			t.Error("positive timeout should produce a distinct deploy client")
		}
		if c.uploadHTTP().Timeout.Seconds() != 300 {
			t.Errorf("deploy timeout = %v, want 5m", c.uploadHTTP().Timeout)
		}
	})
}

// TestNewUUIDv4_Shape pins the v4 contract. Random v4 UUIDs must have
// version=4 and variant=10 — without those, the server-side cache key
// could collide with non-v4 strings.
func TestNewUUIDv4_Shape(t *testing.T) {
	for i := 0; i < 16; i++ {
		got := newUUIDv4()
		if !uuidV4Shape.MatchString(got) {
			t.Errorf("newUUIDv4() = %q, not UUID v4 shape", got)
		}
	}
}

func writeMinimalFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.WriteString(f, strings.Repeat("x", 64))
	return err
}
