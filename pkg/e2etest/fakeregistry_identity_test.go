package e2etest

// adr: 595

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"
)

func TestFakeImagesBindUncompressedLayerIdentity(t *testing.T) {
	constructors := map[string]func() (fakeImage, string){
		"hello":      func() (fakeImage, string) { return HelloImage("test/hello", "body") },
		"above base": func() (fakeImage, string) { return HelloImageAboveBase("test/hello", "body") },
		"base":       func() (fakeImage, string) { return BaseLayerImage("test/base", "body") },
		"process":    func() (fakeImage, string) { return HelloImageWithProcessContract("test/hello", "body", "1000") },
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			img, _ := construct()
			var config struct {
				RootFS struct {
					DiffIDs []string `json:"diff_ids"`
				} `json:"rootfs"`
			}
			if err := json.Unmarshal(img.configBytes, &config); err != nil {
				t.Fatal(err)
			}
			if len(config.RootFS.DiffIDs) != len(img.layerBlobs) {
				t.Fatal("layer and DiffID counts differ")
			}
			for i, blob := range img.layerBlobs {
				z, err := gzip.NewReader(bytes.NewReader(blob.bytes))
				if err != nil {
					t.Fatal(err)
				}
				h := sha256.New()
				if _, err := io.Copy(h, z); err != nil {
					t.Fatal(err)
				}
				if err := z.Close(); err != nil {
					t.Fatal(err)
				}
				if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != config.RootFS.DiffIDs[i] {
					t.Fatalf("layer %d: DiffID=%s, decoded digest=%s", i, config.RootFS.DiffIDs[i], got)
				}
			}
		})
	}
}
