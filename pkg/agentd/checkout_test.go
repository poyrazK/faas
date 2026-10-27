// adr: 20261027090010
package agentd_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

const commit = "0123456789abcdef0123456789abcdef01234567"

// fakeSource serves one archive per call, or fails.
type fakeSource struct {
	archive   []byte
	truncated bool
	resolved  string
	err       error
	calls     int
}

func (f *fakeSource) StreamSourceRef(_ context.Context, _ string, installationID int64, repo, ref string, maxBytes int64) (*githubdgrpc.StreamSourceRefResult, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if installationID != 42 || repo != "acme/app" || ref != commit || maxBytes < 1 {
		return nil, status.Error(codes.InvalidArgument, "unexpected request")
	}
	resolved := f.resolved
	if resolved == "" {
		resolved = commit
	}
	return &githubdgrpc.StreamSourceRefResult{Body: io.NopCloser(bytes.NewReader(f.archive)),
		Stats: &githubdgrpc.StreamSourceRefStats{Truncated: f.truncated, ResolvedCommitSHA: resolved}}, nil
}

func repoArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "package main\n"
	if err := tw.WriteHeader(&tar.Header{Name: "acme-app-0123456/main.go", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checkoutClaim() agentstore.Claim {
	return agentstore.Claim{RunID: "run-1", AccountID: "11111111-2222-3333-4444-555555555555", Plan: "pro",
		Checkout: &agentstore.Checkout{Repository: "acme/app", Commit: commit, InstallationID: 42}}
}

func checkouts(t *testing.T, source *fakeSource) (*agentd.Checkouts, storage.StorageBackend) {
	t.Helper()
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &agentd.Checkouts{Source: source, Storage: backend, TempDir: t.TempDir()}, backend
}

func TestCheckoutIsFetchedOnceAndRoundTrips(t *testing.T) {
	source := &fakeSource{archive: repoArchive(t)}
	c, backend := checkouts(t, source)
	ctx := context.Background()
	key, checkout, err := c.Prepare(ctx, checkoutClaim())
	if err != nil {
		t.Fatal(err)
	}
	if !agentrunproto.ValidInputKey(key, checkoutClaim().AccountID) || !strings.HasSuffix(key, checkout.SHA256+".img") || checkout.Bytes != int64(len(source.archive)) {
		t.Fatalf("key %q checkout %+v", key, checkout)
	}
	again, checkoutAgain, err := c.Prepare(ctx, checkoutClaim())
	if err != nil || again != key || checkoutAgain != checkout || source.calls != 1 {
		t.Fatalf("second prepare %q %+v %v, fetches %d", again, checkoutAgain, err, source.calls)
	}
	r, err := backend.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	image, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := agentrunproto.OpenInputArchive(bytes.NewReader(image), checkout)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "repo")
	if err := agentrunproto.ExtractCheckout(archive, dir, checkout); err != nil {
		t.Fatal(err)
	}
}

func TestCheckoutFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		source *fakeSource
		code   string // empty: transient
	}{
		"too large":      {&fakeSource{archive: []byte("x"), truncated: true}, "checkout_too_large"},
		"not found":      {&fakeSource{err: status.Error(codes.NotFound, "no install")}, "checkout_failed"},
		"other commit":   {&fakeSource{archive: []byte("x"), resolved: strings.Repeat("f", 40)}, "checkout_failed"},
		"empty":          {&fakeSource{archive: nil}, "checkout_failed"},
		"githubd is out": {&fakeSource{err: status.Error(codes.Unavailable, "down")}, ""},
	} {
		c, _ := checkouts(t, tc.source)
		_, _, err := c.Prepare(context.Background(), checkoutClaim())
		var final *agentd.CheckoutError
		switch {
		case err == nil:
			t.Errorf("%s: prepared", name)
		case tc.code == "" && errors.As(err, &final):
			t.Errorf("%s: a transient error was final: %v", name, err)
		case tc.code != "" && (!errors.As(err, &final) || final.Code != tc.code):
			t.Errorf("%s: %v, want %s", name, err, tc.code)
		}
	}
}
