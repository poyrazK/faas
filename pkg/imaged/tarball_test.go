package imaged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// gzTar is a small helper that produces a real gzipped tarball containing
// one file per `name` with body `body`. Used to feed fake layer blobs into
// the imaged handler's ManifestPuller so the build path stays honest.
func gzTar(t *testing.T, files map[string]string) []byte {
	return gzTarWithModes(t, files, nil)
}

func gzTarWithModes(t *testing.T, files map[string]string, modes map[string]int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, body := range files {
		mode := int64(0o644)
		if selected, ok := modes[name]; ok {
			mode = selected
		}
		hdr := &tar.Header{
			Name:     name,
			Mode:     mode,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
