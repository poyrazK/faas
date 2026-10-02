package rootfs

// adr: 431

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

func verifiedRootfsLayer(t *testing.T, mode string) (*imagechain.LayerStream, []byte) {
	t.Helper()
	body, err := io.ReadAll(gzLayer(t, []entry{{name: "etc/passwd", body: "root:x:0:0:root:/root:/bin/sh\n"}, {name: "app/server", body: "#!/bin/sh\n", mode: 0o755}}))
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(z)
	_ = z.Close()
	if err != nil {
		t.Fatal(err)
	}
	diff := imagechain.Digest(plain)
	switch mode {
	case "wrong DiffID":
		diff = imagechain.Digest([]byte("other"))
	case "bad CRC":
		body[len(body)-8] ^= 1
	case "truncated footer":
		body = body[:len(body)-5]
	}
	descriptor := imagechain.Descriptor{Digest: imagechain.Digest(body), Size: int64(len(body))}
	stream, err := imagechain.NewLayerStream(t.Context(), io.NopCloser(bytes.NewReader(body)), descriptor, diff, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return stream, body
}

func TestBuilderVerifiesLayerBeforePublishing(t *testing.T) {
	for _, full := range []bool{false, true} {
		for _, mode := range []string{"complete", "wrong DiffID", "bad CRC", "truncated footer"} {
			t.Run(mode+map[bool]string{false: "/app-layer", true: "/full-rootfs"}[full], func(t *testing.T) {
				stream, body := verifiedRootfsLayer(t, mode)
				guest := filepath.Join(t.TempDir(), "guest-init")
				if err := os.WriteFile(guest, []byte("INIT"), 0o755); err != nil {
					t.Fatal(err)
				}
				run := &mkfsFakeRunner{fill: []byte("EXT4-OUTPUT")}
				builder := NewBuilder(run)
				store := newTestStorage(t)
				key := "apps/test/deploy.ext4"
				var result BuildResult
				var err error
				if full {
					result, err = builder.BuildFullRootfs(t.Context(), BuildFullRootfsInput{Layers: []io.Reader{stream}, Manifest: api.AppManifest{Entrypoint: []string{"/app/server"}}, GuestInitPath: guest, Plan: api.PlanPro, Storage: store, StorageKey: key})
				} else {
					result, err = builder.Build(t.Context(), BuildInput{Layers: []io.Reader{stream}, Manifest: api.AppManifest{Entrypoint: []string{"/app/server"}}, GuestInitPath: guest, Plan: api.PlanPro, Storage: store, StorageKey: key})
				}
				if mode == "complete" {
					proof, e := stream.Consumption()
					if err != nil || e != nil || proof.Digest != imagechain.Digest(body) || proof.CompressedBytes != int64(len(body)) || result.ArtifactDigest != imagechain.Digest(run.fill) {
						t.Fatalf("verified conversion: %+v %v proof=%+v %v", result, err, proof, e)
					}
				} else {
					if err == nil || len(run.argv) != 0 {
						t.Fatalf("bad stream reached mkfs: %v argv=%v", err, run.argv)
					}
					if _, e := stream.Consumption(); e == nil {
						t.Fatal("bad stream acquired consumption")
					}
					if rc, e := store.Get(t.Context(), key); e == nil {
						_ = rc.Close()
						t.Fatal("bad stream published artifact")
					}
				}
			})
		}
	}
}
