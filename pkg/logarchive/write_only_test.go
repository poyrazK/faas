package logarchive

// adr: 20261110210000

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// Slice 3l: a part upload is create-only. An existing object answers 412
// (GCS with x-goog-if-generation-match: 0), which is this upload from an
// earlier attempt, not a failure.
func TestPutObjectIfAbsent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		gcs        bool
		status     int
		wantHeader string
		created    bool
		wantErr    bool
	}{
		{name: "s3 create", status: 200, created: true},
		{name: "gcs create", gcs: true, status: 200, wantHeader: "0", created: true},
		{name: "gcs exists", gcs: true, status: 412, wantHeader: "0"},
		{name: "denied", gcs: true, status: 403, wantHeader: "0", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var header string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				header = r.Header.Get("x-goog-if-generation-match")
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			c, err := NewS3Client(srv.URL, "auto", "b", "AKIA", "secret")
			if err != nil {
				t.Fatal(err)
			}
			if tc.gcs {
				c.KeyID, c.Secret = "", ""
				c.TokenSource = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"})
			}
			created, err := c.PutObjectIfAbsent(context.Background(), "faas-logs/i/2026/11/2026-11-16/p.jsonl.gz", "application/gzip", bytes.NewReader([]byte("x")), 1)
			if (err != nil) != tc.wantErr || created != tc.created {
				t.Fatalf("created=%v err=%v, want created=%v err=%v", created, err, tc.created, tc.wantErr)
			}
			if header != tc.wantHeader {
				t.Fatalf("x-goog-if-generation-match = %q, want %q", header, tc.wantHeader)
			}
		})
	}
}

func TestIsGCSEndpoint(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"https://storage.googleapis.com":     true,
		"https://s3.gra.io.cloud.ovh.net":    false,
		"https://x.r2.cloudflarestorage.com": false,
	} {
		if got := (&S3Client{Endpoint: endpoint}).isGCS(); got != want {
			t.Errorf("isGCS(%s) = %v, want %v", endpoint, got, want)
		}
	}
}

// A write-only envelope makes the daemon ship part objects and marks it as
// holding no read access; an unknown access value refuses.
func TestWriteOnlyEnvelope(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "creds.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	creds, err := ReadCredentials(write(`{"bucket":"b","auth_mode":"gcp_metadata","access":"write-only"}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{}.WithCredentials(creds)
	if !cfg.WriteOnly || !cfg.PartObjects {
		t.Fatalf("write-only envelope: WriteOnly=%v PartObjects=%v", cfg.WriteOnly, cfg.PartObjects)
	}
	creds, err = ReadCredentials(write(`{"bucket":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg := (Config{}).WithCredentials(creds); cfg.WriteOnly || cfg.PartObjects {
		t.Fatalf("default envelope: WriteOnly=%v PartObjects=%v", cfg.WriteOnly, cfg.PartObjects)
	}
	if _, err := ReadCredentials(write(`{"bucket":"b","access":"read"}`)); err == nil || !strings.Contains(err.Error(), "write-only") {
		t.Fatalf("unknown access = %v", err)
	}
}
