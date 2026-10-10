package fcvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

// Overlapped wake network setup.
//
// setupNetwork (netns, veth, TAP, nftables: ~60 ms p50 in production) used to
// run to completion before bringUp staged anything. Nothing a restore or cold
// boot stages before the jailer starts — chroot, artifact resolution, drive
// clones, pre-boot files, snapshot binds, vsock listeners — enters the
// namespace, so Manager.wake now builds it concurrently and the VMM joins it
// immediately before startJailer, the first step that needs it (--netns).
// The namespace still exists before any guest code runs, so isolation and the
// network-ready hook ordering relative to the guest are unchanged.

// errWakeNetwork marks a boot that failed because its overlapped network
// setup failed. bringUp must not answer it with a cold-boot fallback: the
// fallback needs the same namespace.
var errWakeNetwork = errors.New("wake network setup failed")

type wakeNetwork struct {
	done     chan struct{}
	err      error
	hit      bool
	duration time.Duration
}

type wakeNetworkKey struct{}

// startWakeNetwork builds nc in the background. A nil result means the wake
// has no network to build (execution-only); every method accepts nil.
func (m *Manager) startWakeNetwork(ctx context.Context, nc netns.Config, prepared *preparedNetworkEntry, onReady func()) *wakeNetwork {
	n := &wakeNetwork{done: make(chan struct{})}
	go func() {
		defer close(n.done)
		started := time.Now()
		n.hit, n.err = m.setupWakeNetwork(ctx, nc, prepared)
		n.duration = time.Since(started)
		if n.err == nil && onReady != nil {
			onReady()
		}
	}()
	return n
}

// join blocks until the background setup finished, whatever the boot's
// context says: cleanup must never race a half-built namespace.
func (n *wakeNetwork) join() error {
	if n == nil {
		return nil
	}
	<-n.done
	return n.err
}

func withWakeNetwork(ctx context.Context, n *wakeNetwork) context.Context {
	if n == nil {
		return ctx
	}
	return context.WithValue(ctx, wakeNetworkKey{}, n)
}

func wakeNetworkFrom(ctx context.Context) *wakeNetwork {
	n, _ := ctx.Value(wakeNetworkKey{}).(*wakeNetwork)
	return n
}

// awaitWakeNetwork blocks until the overlapped wake network is ready and
// returns how long the caller waited plus the setup's own duration. A boot
// that was not started by an overlapped wake returns immediately.
func awaitWakeNetwork(ctx context.Context) (waited, setup time.Duration, err error) {
	n := wakeNetworkFrom(ctx)
	if n == nil {
		return 0, 0, nil
	}
	started := time.Now()
	select {
	case <-n.done:
	case <-ctx.Done():
		return time.Since(started), 0, fmt.Errorf("vmm: wait for wake network: %w", ctx.Err())
	}
	if n.err != nil {
		return time.Since(started), n.duration, fmt.Errorf("%w: %w", errWakeNetwork, n.err)
	}
	return time.Since(started), n.duration, nil
}

// wakeNetworkJoiner marks a VMM that calls awaitWakeNetwork before the first
// step that enters the instance namespace. Manager overlaps the network build
// only for such VMMs; test fakes keep the serial order they assert on.
type wakeNetworkJoiner interface {
	joinsWakeNetwork()
}

func (*JailerVMM) joinsWakeNetwork() {}
