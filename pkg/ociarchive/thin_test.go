package ociarchive_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/ociarchive/ociarchivetest"
)

// adr: 20261127120000

// reuseOf is the digest → size map of an inspected base's layers.
func reuseOf(t *testing.T, base []byte) map[string]int64 {
	t.Helper()
	summary, err := ociarchive.Inspect(bytes.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}
	reuse := map[string]int64{}
	for _, l := range summary.LayerDescriptors {
		reuse[l.Digest] = l.Size
	}
	return reuse
}

func thin(t *testing.T, full []byte, reuse map[string]int64) ([]byte, int) {
	t.Helper()
	var out bytes.Buffer
	omitted, _, err := ociarchive.Thin(bytes.NewReader(full), &out, reuse)
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), omitted
}

func rebuild(t *testing.T, thinArchive, base []byte) ([]byte, int, error) {
	t.Helper()
	idx, err := ociarchive.IndexBase(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	n, err := ociarchive.Rebuild(bytes.NewReader(thinArchive), idx, &out)
	return out.Bytes(), n, err
}

func TestThinRebuildRoundTrip(t *testing.T) {
	for name, tc := range map[string]struct {
		base, next []string
		reused     int
	}{
		"top layer changed":    {base: []string{"os", "runtime", "app v1"}, next: []string{"os", "runtime", "app v2"}, reused: 2},
		"nothing shared":       {base: []string{"other"}, next: []string{"os", "app"}, reused: 0},
		"everything shared":    {base: []string{"os", "app"}, next: []string{"os", "app"}, reused: 2},
		"repeated layer":       {base: []string{"os"}, next: []string{"os", "app", "os"}, reused: 1},
		"base layer reordered": {base: []string{"app", "os"}, next: []string{"os", "app2"}, reused: 1},
	} {
		t.Run(name, func(t *testing.T) {
			base, _ := ociarchivetest.CanonicalLayers(t, tc.base...)
			full, digest := ociarchivetest.CanonicalLayers(t, tc.next...)
			thinArchive, omitted := thin(t, full, reuseOf(t, base))
			if omitted != tc.reused {
				t.Fatalf("omitted %d layers, want %d", omitted, tc.reused)
			}
			got, fromBase, err := rebuild(t, thinArchive, base)
			if err != nil {
				t.Fatal(err)
			}
			if fromBase != tc.reused {
				t.Errorf("took %d layers from the base, want %d", fromBase, tc.reused)
			}
			if !bytes.Equal(got, full) {
				t.Fatal("the rebuilt archive differs from the full one")
			}
			summary, err := ociarchive.Inspect(bytes.NewReader(got))
			if err != nil || summary.ManifestDigest != digest {
				t.Fatalf("inspect rebuilt: %v %+v", err, summary)
			}
		})
	}
}

func TestThinKeepsMetadata(t *testing.T) {
	full, _ := ociarchivetest.CanonicalLayers(t, "os", "app")
	// Offer every blob, manifest and config included: only layers go.
	reuse := map[string]int64{}
	tr := tar.NewReader(bytes.NewReader(full))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(hdr.Name) > len("blobs/sha256/") {
			reuse["sha256:"+hdr.Name[len("blobs/sha256/"):]] = hdr.Size
		}
	}
	thinArchive, omitted := thin(t, full, reuse)
	if omitted != 2 {
		t.Fatalf("omitted %d, want the 2 layers", omitted)
	}
	got, _, err := rebuild(t, thinArchive, full)
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("rebuild: %v", err)
	}
}

func TestRebuildRefuses(t *testing.T) {
	base, _ := ociarchivetest.CanonicalLayers(t, "os", "app v1")
	full, _ := ociarchivetest.CanonicalLayers(t, "os", "app v2")
	thinArchive, _ := thin(t, full, reuseOf(t, base))

	t.Run("base lacks the layer", func(t *testing.T) {
		other, _ := ociarchivetest.CanonicalLayers(t, "other")
		if _, _, err := rebuild(t, thinArchive, other); !errors.Is(err, ociarchive.ErrBaseMissing) {
			t.Fatalf("err = %v, want ErrBaseMissing", err)
		}
	})
	t.Run("blob that is not a layer", func(t *testing.T) {
		foreign, _ := ociarchivetest.CanonicalLayers(t, "foreign")
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		copyEntries(t, tw, thinArchive)
		copyBlobs(t, tw, foreign)
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := rebuild(t, buf.Bytes(), base); !errors.Is(err, ociarchive.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

// copyEntries copies every entry of archive into tw.
func copyEntries(t *testing.T, tw *tar.Writer, archive []byte) {
	t.Helper()
	copyMatching(t, tw, archive, func(string) bool { return true })
}

// copyBlobs copies archive's layer-sized blobs (not JSON) into tw.
func copyBlobs(t *testing.T, tw *tar.Writer, archive []byte) {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) == 0 || body[0] == '{' || len(hdr.Name) < len("blobs/sha256/") {
			continue
		}
		writeTarEntry(t, tw, hdr.Name, body)
	}
}

func copyMatching(t *testing.T, tw *tar.Writer, archive []byte, keep func(string) bool) {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if keep(hdr.Name) {
			writeTarEntry(t, tw, hdr.Name, body)
		}
	}
}

func writeTarEntry(t *testing.T, tw *tar.Writer, name string, body []byte) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
}
