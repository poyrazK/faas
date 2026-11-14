package signd_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// SignDigest sends the publisher's digest to signd without reading the layer
// back: it signs even before the layer is readable, and the signature then
// verifies against the stored bytes.
func TestClientSignDigestSignsWithoutReadingTheLayer(t *testing.T) {
	f := newFixture(t)
	key := storagekey.AppLayer("alice", depID)
	layer := bytes.Repeat([]byte("layer "), 100)
	sum := sha256.Sum256(layer)
	if err := f.client(node).SignDigest(context.Background(), key, cosign.SigKeyFor(key), hex.EncodeToString(sum[:]), int64(len(layer))); err != nil {
		t.Fatalf("SignDigest: %v", err)
	}
	f.objects.blobs[key] = layer
	if err := f.verify(t, key); err != nil {
		t.Fatalf("signature over the publisher's digest does not verify: %v", err)
	}
}

func TestClientSignDigestKeepsBasesLocal(t *testing.T) {
	f := newFixture(t)
	base := storagekey.BaseForArch("node22", "amd64")
	local := &recordingSigner{}
	c := signd.NewClient(direct{srv: f.srv, node: node}, f.objects, local)
	if err := c.SignDigest(context.Background(), base, cosign.SigKeyFor(base), "00", 1); err != nil || len(local.keys) != 1 {
		t.Fatalf("base layer: err %v, local signs %v", err, local.keys)
	}
	if len(f.store.rows) != 0 {
		t.Fatal("a base layer reached signd")
	}
}
