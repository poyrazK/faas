package rootfs

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

type stagedLayerRunner struct{ argv []string }

func (r *stagedLayerRunner) Run(_ context.Context, argv []string) error {
	r.argv = argv
	return os.WriteFile(argv[len(argv)-2], []byte("ext4"), 0o644)
}

// adr: 20261027090010 — a platform layer is mkfs-ed from its staging tree under its own
// label, then published and signed like a base.
func TestPublishStagedLayerLabelsPublishesAndSigns(t *testing.T) {
	ctx := context.Background()
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, signer := &stagedLayerRunner{}, &appMkfsSigner{}
	b := NewBuilder(run).WithSigner(signer)
	staging := t.TempDir()
	identity, err := b.PublishStagedLayer(ctx, be, "base/agent-layer.ext4", staging, "agent-layer", 512)
	if err != nil {
		t.Fatalf("PublishStagedLayer: %v", err)
	}
	if len(run.argv) < 8 || run.argv[3] != "agent-layer" || run.argv[5] != staging || run.argv[7] != "512M" {
		t.Fatalf("mkfs argv = %q", run.argv)
	}
	rc, err := be.Get(ctx, "base/agent-layer.ext4")
	if err != nil {
		t.Fatalf("published layer: %v", err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != "ext4" || identity.Bytes != 4 || identity.Digest == "" {
		t.Fatalf("published %q with identity %+v", body, identity)
	}
	if signer.calls != 1 {
		t.Fatalf("signer calls = %d, want 1", signer.calls)
	}
	if _, err := os.Stat(run.argv[6]); !os.IsNotExist(err) {
		t.Fatalf("mkfs scratch file left behind: %v", err)
	}
	for _, size := range []int{0, MinLayerMB - 1} {
		if _, err := b.PublishStagedLayer(ctx, be, "base/agent-layer.ext4", staging, "agent-layer", size); err == nil {
			t.Errorf("size %d MiB accepted", size)
		}
	}
	if _, err := b.PublishStagedLayer(ctx, be, "base/agent-layer.ext4", staging, "", 512); err == nil {
		t.Error("unlabelled layer accepted")
	}
}
