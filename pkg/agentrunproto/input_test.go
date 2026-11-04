// adr: 20261027090010
package agentrunproto_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
)

type entry struct {
	name, body, link string
	typ              byte
	mode             int64
}

// commitArchive builds a codeload-shaped archive: a global PAX header, then
// every entry under one top-level directory unless its name is absolute.
func commitArchive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "abc"}}); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Size: int64(len(e.body)), Linkname: e.link}
		if e.typ != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func inputImage(t *testing.T, archive []byte) (*bytes.Reader, agentrunproto.Checkout) {
	t.Helper()
	sum := sha256.Sum256(archive)
	var img bytes.Buffer
	if err := agentrunproto.WriteInputImage(&img, bytes.NewReader(archive), int64(len(archive)), sum); err != nil {
		t.Fatal(err)
	}
	if int64(img.Len()) != agentrunproto.InputImageBytes(int64(len(archive))) || img.Len()%512 != 0 {
		t.Fatalf("image is %d bytes", img.Len())
	}
	return bytes.NewReader(img.Bytes()), agentrunproto.Checkout{SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(archive)),
		MaxExtractedBytes: 1 << 20, MaxEntries: 100}
}

func TestInputDriveRoundTrip(t *testing.T) {
	archive := commitArchive(t,
		entry{name: "acme-app-0123abc/", typ: tar.TypeDir},
		entry{name: "acme-app-0123abc/README.md", body: "hello", typ: tar.TypeReg, mode: 0o644},
		entry{name: "acme-app-0123abc/bin/run", body: "#!/bin/sh", typ: tar.TypeReg, mode: 0o755},
		entry{name: "acme-app-0123abc/docs/link", link: "../README.md", typ: tar.TypeSymlink},
		entry{name: "acme-app-0123abc/../escape", body: "x", typ: tar.TypeReg, mode: 0o644},
		entry{name: "/etc/passwd", body: "x", typ: tar.TypeReg, mode: 0o644},
	)
	drive, checkout := inputImage(t, archive)
	if err := checkout.Validate(); err != nil {
		t.Fatal(err)
	}
	r, err := agentrunproto.OpenInputArchive(drive, checkout)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "repo")
	if err := agentrunproto.ExtractCheckout(r, dir, checkout); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "README.md")); err != nil || string(b) != "hello" {
		t.Fatalf("README %q %v", b, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "bin/run")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("executable %v %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(dir, "docs/link")); err != nil || target != "../README.md" {
		t.Fatalf("symlink %q %v", target, err)
	}
	for _, outside := range []string{filepath.Join(filepath.Dir(dir), "escape"), filepath.Join(dir, "etc/passwd")} {
		if _, err := os.Stat(outside); err == nil {
			t.Fatalf("%s was written", outside)
		}
	}
}

func TestInputDriveRefusesTheWrongArchive(t *testing.T) {
	archive := commitArchive(t, entry{name: "r/a", body: "a", typ: tar.TypeReg, mode: 0o644})
	drive, checkout := inputImage(t, archive)
	wrong := checkout
	wrong.SHA256 = hex.EncodeToString(make([]byte, sha256.Size))
	if _, err := agentrunproto.OpenInputArchive(drive, wrong); !errors.Is(err, agentrunproto.ErrInputDrive) {
		t.Fatalf("a different digest: %v", err)
	}
	if _, err := agentrunproto.OpenInputArchive(bytes.NewReader(make([]byte, 4096)), checkout); !errors.Is(err, agentrunproto.ErrInputDrive) {
		t.Fatalf("a blank drive: %v", err)
	}
	tampered, _ := inputImage(t, archive)
	raw := make([]byte, tampered.Len())
	_, _ = tampered.ReadAt(raw, 0)
	raw[agentrunproto.InputHeaderBytes+3] ^= 0xff
	if _, err := agentrunproto.OpenInputArchive(bytes.NewReader(raw), checkout); !errors.Is(err, agentrunproto.ErrInputDrive) {
		t.Fatalf("a tampered archive: %v", err)
	}
}

func TestExtractCheckoutBounds(t *testing.T) {
	many := []entry{}
	for _, n := range []string{"a", "b", "c"} {
		many = append(many, entry{name: "r/" + n, body: "1234", typ: tar.TypeReg, mode: 0o644})
	}
	for name, tc := range map[string]struct {
		entries []entry
		limit   func(*agentrunproto.Checkout)
	}{
		"entries": {many, func(c *agentrunproto.Checkout) { c.MaxEntries = 2 }},
		"bytes":   {many, func(c *agentrunproto.Checkout) { c.MaxExtractedBytes = 10 }},
		"symlink escape": {[]entry{{name: "r/out", link: "/", typ: tar.TypeSymlink}, {name: "r/out/tmp/x", body: "x", typ: tar.TypeReg, mode: 0o644}},
			func(*agentrunproto.Checkout) {}},
	} {
		archive := commitArchive(t, tc.entries...)
		drive, checkout := inputImage(t, archive)
		tc.limit(&checkout)
		r, err := agentrunproto.OpenInputArchive(drive, checkout)
		if err != nil {
			t.Fatal(err)
		}
		if err := agentrunproto.ExtractCheckout(r, filepath.Join(t.TempDir(), "repo"), checkout); err == nil {
			t.Errorf("%s: extracted", name)
		}
	}
}

// Every plan's largest checkout fits the fixed input drive.
func TestInputDriveHoldsEveryPlansArchive(t *testing.T) {
	for _, limit := range api.AgentCheckoutArchiveMaxBytes {
		if agentrunproto.InputImageBytes(limit) > agentrunproto.InputDriveBytes {
			t.Fatalf("a %d-byte archive needs %d bytes; the drive holds %d", limit, agentrunproto.InputImageBytes(limit), agentrunproto.InputDriveBytes)
		}
	}
}
