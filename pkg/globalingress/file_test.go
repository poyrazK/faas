//go:build linux || darwin

package globalingress

// adr: 656
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

func TestManifestRefusesUnsafeFileShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "routes.json")
	if err := os.WriteFile(path, []byte("signed content"), 0o640); err != nil {
		t.Fatal(err)
	}
	if raw, err := ReadManifest(path); err != nil || string(raw) != "signed content" {
		t.Fatal(string(raw), err)
	}
	if _, err := ReadManifest("relative.json"); err == nil {
		t.Fatal("relative handoff accepted")
	}
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("directory accepted")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(link); err == nil {
		t.Fatal("symlink accepted")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(fifo); err == nil {
		t.Fatal("FIFO accepted")
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(path); err == nil {
		t.Fatal("unprotected handoff accepted")
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(dir, "hardlink")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(path); err == nil {
		t.Fatal("mutable hardlink accepted")
	}
}

func TestManifestRefreshQuarantineAndCancellation(t *testing.T) {
	g, s, signer := fixture(t, nil, "https://a.example.com")
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(path, encode(t, s, signer), 0o640); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := g.Start(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, _, err := g.target(s.Routes[0].GlobalAppID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("invalid"), 0o640); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * api.GlobalIngressRefreshInterval)
	for {
		_, _, err := g.target(s.Routes[0].GlobalAppID)
		if errors.Is(err, ErrUnavailable) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not quarantine source")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Generation++
	replacement := path + ".next"
	if err := os.WriteFile(replacement, encode(t, s, signer), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * api.GlobalIngressRefreshInterval)
	for {
		_, _, err := g.target(s.Routes[0].GlobalAppID)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("verified replacement did not recover")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	stop() // Joins the worker and remains safe on repeated cleanup.
	if _, err := g.Start(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled startup accepted", err)
	}
}

func TestManifestRequiresInitialVerifiedProjection(t *testing.T) {
	g, _, _ := fixture(t, nil, "https://a.example.com")
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(path, []byte("invalid"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Start(context.Background(), path); err == nil {
		t.Fatal("unverified initial routes accepted")
	}
}
