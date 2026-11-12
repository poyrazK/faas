package rootfs

// adr: 20261112170911

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRepublishPublishesAndSignsTheRecordedBytes(t *testing.T) {
	ctx := context.Background()
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const body = "published ext4 bytes"
	src := filepath.Join(t.TempDir(), "src.ext4")
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
	recorded := PublishedLayer{Path: src, Digest: digest, Bytes: int64(len(body))}

	signer := &digestRecordingSigner{}
	identity, err := NewBuilder(nil).WithSigner(signer).Republish(ctx, be, "apps/a/new.ext4", recorded)
	if err != nil {
		t.Fatal(err)
	}
	if identity != (ArtifactIdentity{Digest: digest, Bytes: int64(len(body))}) {
		t.Fatalf("identity = %+v", identity)
	}
	if signer.digests != 1 || signer.signs != 0 || "sha256:"+signer.digestHex != digest {
		t.Fatalf("signed %d by digest (%q), %d by reading; want the republished digest once", signer.digests, signer.digestHex, signer.signs)
	}
	rc, err := be.Get(ctx, "apps/a/new.ext4")
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(stored) != body {
		t.Fatalf("stored %q, want the source bytes", stored)
	}

	t.Run("bytes that are not the recorded ones", func(t *testing.T) {
		signer := &digestRecordingSigner{}
		wrong := recorded
		wrong.Digest = "sha256:" + strings.Repeat("0", 64)
		if _, err := NewBuilder(nil).WithSigner(signer).Republish(ctx, be, "apps/a/bad.ext4", wrong); err == nil {
			t.Fatal("republished bytes whose digest differs from the record")
		}
		if signer.digests+signer.signs != 0 {
			t.Fatal("signed a layer that is not the recorded artifact")
		}
		if rc, err := be.Get(ctx, "apps/a/bad.ext4"); err == nil {
			_ = rc.Close()
			t.Fatal("left the mismatched bytes under the new key")
		}
	})
	t.Run("no recorded identity", func(t *testing.T) {
		if _, err := NewBuilder(nil).Republish(ctx, be, "apps/a/x.ext4", PublishedLayer{Path: src}); err == nil {
			t.Fatal("republished without a recorded identity")
		}
	})
}
