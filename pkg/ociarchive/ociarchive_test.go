package ociarchive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261127120000

type entry struct {
	name string
	body []byte
}

func tarBytes(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func layerTar(t *testing.T, file string) []byte {
	return tarBytes(t, entry{file, []byte("hello from " + file)})
}

func imageConfig(t *testing.T, arch string, layers ...[]byte) []byte {
	t.Helper()
	var ids []string
	for _, l := range layers {
		ids = append(ids, digestOf(l))
	}
	b, err := json.Marshal(map[string]any{
		"os": "linux", "architecture": arch,
		"config": map[string]any{"Cmd": []string{"/app"}, "ExposedPorts": map[string]any{"3000/tcp": map[string]any{}}},
		"rootfs": map[string]any{"type": "layers", "diff_ids": ids},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// legacyDockerSave is the pre-25 `docker save` layout: manifest.json plus
// <id>/layer.tar uncompressed layers.
func legacyDockerSave(t *testing.T, arch string) []byte {
	l1, l2 := layerTar(t, "a"), layerTar(t, "b")
	config := imageConfig(t, arch, l1, l2)
	manifest, _ := json.Marshal([]dockerSaveEntry{{Config: "cfg.json", RepoTags: []string{"myapp:latest"}, Layers: []string{"l1/layer.tar", "l2/layer.tar"}}})
	return tarBytes(t, entry{"l2/layer.tar", l2}, entry{"cfg.json", config}, entry{"l1/layer.tar", l1}, entry{"manifest.json", manifest})
}

// ociDockerSave is Docker 25+ with the containerd store: index.json names an
// image index whose linux/amd64 child has gzip layers.
func ociDockerSave(t *testing.T) []byte {
	raw := layerTar(t, "a")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	config := imageConfig(t, "amd64", raw)
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": MediaTypeOCIManifest,
		"config": map[string]any{"mediaType": MediaTypeOCIConfig, "digest": digestOf(config), "size": len(config)},
		"layers": []any{map[string]any{"mediaType": MediaTypeLayerGzip, "digest": digestOf(gz.Bytes()), "size": gz.Len()}}})
	list, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": MediaTypeOCIIndex, "manifests": []any{
		map[string]any{"mediaType": MediaTypeOCIManifest, "digest": "sha256:" + strings.Repeat("0", 64), "size": 10, "platform": map[string]any{"os": "linux", "architecture": "arm64"}},
		map[string]any{"mediaType": MediaTypeOCIManifest, "digest": digestOf(manifest), "size": len(manifest), "platform": map[string]any{"os": "linux", "architecture": "amd64"}},
	}})
	idx, _ := json.Marshal(map[string]any{"schemaVersion": 2, "manifests": []any{
		map[string]any{"mediaType": MediaTypeOCIIndex, "digest": digestOf(list), "size": len(list)}}})
	return tarBytes(t, entry{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)}, entry{"index.json", idx},
		entry{blobPath(digestOf(list)), list}, entry{blobPath(digestOf(manifest)), manifest},
		entry{blobPath(digestOf(config)), config}, entry{blobPath(digestOf(gz.Bytes())), gz.Bytes()})
}

func convert(t *testing.T, input []byte, want string) (ConvertResult, []byte, error) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.tar")
	if err := os.WriteFile(in, input, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	res, err := Convert(in, want, dir, &out)
	if left, _ := filepath.Glob(filepath.Join(dir, "gregale-layer-*")); len(left) != 0 {
		t.Fatalf("staged layers left behind: %v", left)
	}
	return res, out.Bytes(), err
}

func TestConvertThenInspect(t *testing.T) {
	cases := map[string]struct {
		input  []byte
		layers int
		name   string
	}{
		"legacy docker save": {legacyDockerSave(t, "amd64"), 2, "myapp:latest"},
		"docker 25 oci":      {ociDockerSave(t), 1, ""},
	}
	// A tag naming the only image (or none, as `docker save <id>` writes) is
	// not a filter.
	if _, _, err := convert(t, legacyDockerSave(t, "amd64"), "other:1"); err != nil {
		t.Fatalf("single image with an unrelated tag: %v", err)
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, archive, err := convert(t, tc.input, "")
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if res.Layers != tc.layers || res.ImageName != tc.name {
				t.Fatalf("result %+v", res)
			}
			sum, err := Inspect(bytes.NewReader(archive))
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if sum.ManifestDigest != res.ManifestDigest || sum.Layers != tc.layers || sum.ExposedPort != 3000 {
				t.Fatalf("summary %+v, convert %+v", sum, res)
			}
			// Deterministic: a resumed upload keeps its identity.
			_, again, _ := convert(t, tc.input, "")
			if !bytes.Equal(archive, again) {
				t.Fatal("conversion is not deterministic")
			}
		})
	}
}

func TestConvertRefusals(t *testing.T) {
	l := layerTar(t, "a")
	badDiff := imageConfig(t, "amd64", layerTar(t, "other"))
	manifest, _ := json.Marshal([]dockerSaveEntry{{Config: "cfg.json", Layers: []string{"l/layer.tar"}}})
	two, _ := json.Marshal([]dockerSaveEntry{
		{Config: "cfg.json", RepoTags: []string{"a:1"}, Layers: []string{"l/layer.tar"}},
		{Config: "cfg.json", RepoTags: []string{"b:1"}, Layers: []string{"l/layer.tar"}}})
	twoImages := tarBytes(t, entry{"cfg.json", imageConfig(t, "amd64", l)}, entry{"l/layer.tar", l}, entry{"manifest.json", two})
	cases := map[string]struct {
		input []byte
		want  string
		err   string
	}{
		"arm64 image":       {legacyDockerSave(t, "arm64"), "", "linux/arm64"},
		"wrong tag":         {twoImages, "other:1", "no image tagged"},
		"two images":        {twoImages, "", "holds 2 images"},
		"diff id mismatch":  {tarBytes(t, entry{"cfg.json", badDiff}, entry{"l/layer.tar", l}, entry{"manifest.json", manifest}), "", "diff ID"},
		"missing layer":     {tarBytes(t, entry{"cfg.json", imageConfig(t, "amd64", l)}, entry{"manifest.json", manifest}), "", "missing"},
		"not a docker save": {tarBytes(t, entry{"README", []byte("x")}), "", "not a docker save"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := convert(t, tc.input, tc.want)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("err = %v, want %q", err, tc.err)
			}
		})
	}
}

func TestInspectRefusals(t *testing.T) {
	_, good, err := convert(t, legacyDockerSave(t, "amd64"), "")
	if err != nil {
		t.Fatal(err)
	}
	withExtra := append(bytes.TrimSuffix(good, make([]byte, 1024)), tarBytes(t, entry{"etc/passwd", []byte("root")})...)
	tampered := bytes.Replace(good, []byte("hello"), []byte("HELLO"), 1) // inside a gzip blob it never matches; corrupt config instead
	tampered = bytes.Replace(tampered, []byte(`"architecture":"amd64"`), []byte(`"architecture":"amd65"`), 1)
	cases := map[string]struct {
		input []byte
		err   string
	}{
		"extra entry":    {withExtra, "unexpected entry"},
		"tampered blob":  {tampered, "does not match its digest"},
		"source tarball": {tarBytes(t, entry{"main.go", []byte("package main")}), "unexpected entry"},
		"no index":       {tarBytes(t, entry{"oci-layout", []byte("{}")}), "index.json are required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Inspect(bytes.NewReader(tc.input))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("err = %v, want %q", err, tc.err)
			}
		})
	}
}
