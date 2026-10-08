package cosign

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gatedLayerStorage holds every read of one key until release closes, and
// counts those reads.
type gatedLayerStorage struct {
	*memStorage
	gated   string
	reads   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedLayerStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == g.gated {
		g.reads.Add(1)
		g.once.Do(func() { close(g.started) })
		<-g.release
	}
	return g.memStorage.Get(ctx, key)
}

// production-us hunt #6 (H5-56): the first wakes of an app after a schedd
// restart each streamed and hashed the whole layer, alongside the background
// warm hashing the same layer. Concurrent verifications now share one read,
// and a caller that gives up does not abort it for the others.
func TestLocalVerifier_ConcurrentVerifiesShareOneRead(t *testing.T) {
	ctx := context.Background()
	privPath, _ := keyPairTempDir(t)
	const layerKey = "apps/test/00000000-0000-0000-0000-000000000000.ext4"
	sigKey := SigKeyFor(layerKey)
	mem := newMemStorage()
	artifact := make([]byte, 4096)
	if _, err := rand.Read(artifact); err != nil {
		t.Fatal(err)
	}
	if err := mem.Put(ctx, layerKey, bytes.NewReader(artifact)); err != nil {
		t.Fatal(err)
	}
	signer, err := NewLocalSigner(privPath, mem, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Sign(ctx, layerKey, sigKey); err != nil {
		t.Fatal(err)
	}
	stor := &gatedLayerStorage{memStorage: mem, gated: layerKey, started: make(chan struct{}), release: make(chan struct{})}
	verifier, err := NewLocalVerifier(filepath.Join(filepath.Dir(privPath), "sign-pub.pem"), stor)
	if err != nil {
		t.Fatal(err)
	}

	// The caller that starts the shared read gives up while it is in flight.
	giveUpCtx, giveUp := context.WithCancel(ctx)
	gaveUp := make(chan error, 1)
	go func() { gaveUp <- verifier.Verify(giveUpCtx, layerKey, sigKey) }()
	<-stor.started

	const waiters = 4
	results := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() { results <- verifier.Verify(ctx, layerKey, sigKey) }()
	}
	giveUp()
	select {
	case err := <-gaveUp:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned caller got %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abandoned caller stayed blocked on the shared read")
	}

	close(stor.release)
	for i := 0; i < waiters; i++ {
		if err := <-results; err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}
	if got := stor.reads.Load(); got != 1 {
		t.Fatalf("layer read %d times, want 1 shared read", got)
	}
	// The shared result was cached: even a cancelled caller now passes
	// without another read.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := verifier.Verify(cancelled, layerKey, sigKey); err != nil {
		t.Fatalf("cached verify: %v", err)
	}
	if got := stor.reads.Load(); got != 1 {
		t.Fatalf("layer read %d times after cache hit, want 1", got)
	}
}
