package imagechain

// adr: 387

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
)

func chainFixture(t *testing.T, index bool) (*Evidence, []byte, []byte) {
	t.Helper()
	plain := append([]byte("tar content"), make([]byte, 1024)...)
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err := z.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	config := []byte(fmt.Sprintf(`{"os":"linux","architecture":"amd64","config":{"Cmd":["/app"]},"rootfs":{"type":"layers","diff_ids":[%q]}}`, Digest(plain)))
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": Descriptor{Digest: Digest(config), Size: int64(len(config))}, "layers": []Descriptor{{Digest: Digest(compressed.Bytes()), Size: int64(compressed.Len())}}})
	e := &Evidence{SourceManifest: manifest, Config: config}
	if index {
		e.SelectedManifest = manifest
		e.SourceManifest = []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":%d,"platform":{"os":"linux","architecture":"amd64"}}]}`, Digest(manifest), len(manifest)))
	}
	return e, compressed.Bytes(), plain
}
func selectedDigest(e *Evidence) string {
	if len(e.SelectedManifest) > 0 {
		return Digest(e.SelectedManifest)
	}
	return Digest(e.SourceManifest)
}

func TestRetainedDirectAndIndexImageChain(t *testing.T) {
	for _, index := range []bool{false, true} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			e, compressed, plain := chainFixture(t, index)
			image, err := Validate(e, Digest(e.SourceManifest), selectedDigest(e))
			if err != nil || len(image.Layers) != 1 || image.Layers[0].Digest != Digest(compressed) || image.DiffIDs[0] != Digest(plain) {
				t.Fatalf("retained chain: %+v %v", image, err)
			}
			clone := e.Clone()
			clone.Config[0] ^= 1
			if _, err := Validate(e, Digest(e.SourceManifest), selectedDigest(e)); err != nil {
				t.Fatalf("clone changed original: %v", err)
			}
		})
	}
}

func TestRetainedChainRejectsSubstitution(t *testing.T) {
	for _, mode := range []string{"source", "selected", "config", "descriptor size", "ambiguous platform", "diff count", "platform"} {
		t.Run(mode, func(t *testing.T) {
			e, _, _ := chainFixture(t, true)
			source, selected := Digest(e.SourceManifest), selectedDigest(e)
			switch mode {
			case "source":
				e.SourceManifest[0] ^= 1
			case "selected":
				e.SelectedManifest[0] ^= 1
			case "config":
				e.Config[0] ^= 1
			case "descriptor size":
				e.SourceManifest = bytes.Replace(e.SourceManifest, []byte(fmt.Sprintf(`"size":%d`, len(e.SelectedManifest))), []byte(`"size":1`), 1)
				source = Digest(e.SourceManifest)
			case "ambiguous platform":
				var index map[string]any
				_ = json.Unmarshal(e.SourceManifest, &index)
				entries := index["manifests"].([]any)
				other := map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": Digest([]byte("other")), "size": 5, "platform": map[string]string{"os": "linux", "architecture": "amd64"}}
				index["manifests"] = append(entries, other)
				e.SourceManifest, _ = json.Marshal(index)
				source = Digest(e.SourceManifest)
			case "diff count", "platform":
				if mode == "diff count" {
					var config map[string]any
					_ = json.Unmarshal(e.Config, &config)
					config["rootfs"] = map[string]any{"type": "layers", "diff_ids": []string{}}
					e.Config, _ = json.Marshal(config)
				} else {
					e.Config = bytes.Replace(e.Config, []byte(`"amd64"`), []byte(`"arm64"`), 1)
				}
				var m map[string]any
				_ = json.Unmarshal(e.SelectedManifest, &m)
				m["config"] = Descriptor{Digest: Digest(e.Config), Size: int64(len(e.Config))}
				e.SelectedManifest, _ = json.Marshal(m)
				selected = Digest(e.SelectedManifest)
				var index map[string]any
				_ = json.Unmarshal(e.SourceManifest, &index)
				entry := index["manifests"].([]any)[0].(map[string]any)
				entry["digest"] = selected
				entry["size"] = len(e.SelectedManifest)
				e.SourceManifest, _ = json.Marshal(index)
				source = Digest(e.SourceManifest)
			}
			if _, err := Validate(e, source, selected); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted %s substitution: %v", mode, err)
			}
		})
	}
}

func TestLayerStreamRequiresCompressedAndUncompressedEOF(t *testing.T) {
	for _, mode := range []string{"complete", "raw only", "partial decompression", "wrong DiffID", "wrong compressed digest", "wrong size", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			e, compressed, plain := chainFixture(t, false)
			image, err := Validate(e, Digest(e.SourceManifest), selectedDigest(e))
			if err != nil {
				t.Fatal(err)
			}
			d, expected := image.Layers[0], image.DiffIDs[0]
			if mode == "wrong DiffID" {
				expected = Digest([]byte("other"))
			}
			if mode == "wrong compressed digest" {
				d.Digest = Digest([]byte("other"))
			}
			if mode == "wrong size" {
				d.Size++
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream, err := NewLayerStream(ctx, io.NopCloser(bytes.NewReader(compressed)), d, expected, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if mode == "raw only" {
				_, _ = io.Copy(io.Discard, stream)
			} else {
				z, err := gzip.NewReader(stream)
				if err == nil {
					reader := stream.VerifyingUncompressedReader(z)
					if mode == "cancelled" {
						cancel()
					}
					if mode == "partial decompression" {
						_, _ = io.CopyN(io.Discard, reader, 3)
					} else {
						_, _ = io.Copy(io.Discard, reader)
					}
					_ = z.Close()
					_, _ = io.Copy(io.Discard, stream)
				}
			}
			got, err := stream.Consumption()
			if mode == "complete" {
				if err != nil || got.Digest != Digest(compressed) || got.DiffID != Digest(plain) || got.CompressedBytes != int64(len(compressed)) || got.UncompressedBytes != int64(len(plain)) {
					t.Fatalf("consumption: %+v %v", got, err)
				}
			} else if err == nil {
				t.Fatalf("%s obtained full consumption: %+v", mode, got)
			}
		})
	}
}
