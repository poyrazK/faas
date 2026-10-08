package fcvm

// adr: 767
// adr: 769
import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	errNativeDaemonOwnershipLost = errors.New("native lifecycle: original daemon ownership descriptor was lost")
	errNativeEffectExpired       = errors.New("native lifecycle: operation context has already released ownership")
	errNativeInventoryNested     = errors.New("native lifecycle: inventory cannot run inside a producer")
	errNativeInventoryMutation   = errors.New("native lifecycle: inventory context cannot perform producer effects")
)

type nativeEffectContextKey struct{}

// A permit belongs to one call, including its deferred cleanup. Nested calls
// acquire their own references while that permit is active. An escaped context
// cannot bypass a later inventory after its original call returned.
type nativeEffectPermit struct {
	barrier   *nativeEffectBarrier
	root      string
	inventory bool
	active    bool // guarded by barrier.mu
	family    *nativeEffectFamily
}

type nativeEffectFamily struct {
	references int // the filesystem lock outlives every nested permit
	unlock     func() error
}

type nativeEffectBarrier struct {
	mu                 sync.Mutex
	changed            chan struct{}
	producers          int
	inventories        int
	waitingInventories int
	poison             error
}

func (b *nativeEffectBarrier) signalLocked() {
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
}

func (b *nativeEffectBarrier) begin(ctx context.Context, root string, inventory bool, lock func(context.Context, bool) (func() error, error)) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if parent, _ := ctx.Value(nativeEffectContextKey{}).(*nativeEffectPermit); parent != nil && parent.root == root {
		return parent.nest(ctx, inventory)
	}
	b.mu.Lock()
	if inventory {
		b.waitingInventories++
		b.signalLocked()
	}
	for {
		if err := errors.Join(ctx.Err(), b.poison); err != nil {
			if inventory {
				b.waitingInventories--
				b.signalLocked()
			}
			b.mu.Unlock()
			return nil, nil, err
		}
		ready := b.inventories == 0 && (!inventory && b.waitingInventories == 0 || inventory && b.producers == 0)
		if ready {
			break
		}
		if b.changed == nil {
			b.changed = make(chan struct{})
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		b.mu.Lock()
	}
	if inventory {
		b.waitingInventories--
		b.inventories++
	} else {
		b.producers++
	}
	p := &nativeEffectPermit{barrier: b, root: root, inventory: inventory, active: true, family: &nativeEffectFamily{references: 1}}
	b.mu.Unlock()
	unlock, err := lock(ctx, inventory)
	if err != nil {
		p.release()
		return nil, nil, err
	}
	p.family.unlock = unlock
	return context.WithValue(ctx, nativeEffectContextKey{}, p), p.release, nil
}

func (p *nativeEffectPermit) nest(ctx context.Context, inventory bool) (context.Context, func(), error) {
	b := p.barrier
	b.mu.Lock()
	defer b.mu.Unlock()
	if !p.active {
		return nil, nil, errNativeEffectExpired
	}
	if err := errors.Join(ctx.Err(), b.poison); err != nil {
		return nil, nil, err
	}
	if inventory != p.inventory {
		if inventory {
			return nil, nil, errNativeInventoryNested
		}
		return nil, nil, errNativeInventoryMutation
	}
	child := &nativeEffectPermit{barrier: b, root: p.root, inventory: inventory, active: true, family: p.family}
	p.family.references++
	if inventory {
		b.inventories++
	} else {
		b.producers++
	}
	return context.WithValue(ctx, nativeEffectContextKey{}, child), child.release, nil
}

func (p *nativeEffectPermit) release() {
	b := p.barrier
	b.mu.Lock()
	defer b.mu.Unlock()
	if !p.active {
		return
	}
	p.active = false
	p.family.references--
	if p.family.references == 0 && p.family.unlock != nil {
		b.poison = errors.Join(b.poison, p.family.unlock())
	}
	if p.inventory {
		b.inventories--
	} else {
		b.producers--
	}
	b.signalLocked()
}

// Detached callbacks must take a fresh producer permit when they actually run.
// Holding an old caller's context past its return is never an exemption.
func detachedNativeEffectContext(ctx context.Context) context.Context {
	return withoutNativeEffectPermit(context.WithoutCancel(ctx))
}

func withoutNativeEffectPermit(ctx context.Context) context.Context {
	return context.WithValue(ctx, nativeEffectContextKey{}, (*nativeEffectPermit)(nil))
}

// The turnstile excludes new outer producers while a reader waits for the
// root-wide exclusive lock, including other VMM objects and daemon processes.
func lockNativeEffects(ctx context.Context, root string, inventory bool) (func() error, error) {
	turnstile, err := lockNativeJournalFile(ctx, filepath.Join(root, ".native-effects-turnstile.lock"))
	if err != nil {
		return nil, err
	}
	var lock *os.File
	if inventory {
		lock, err = lockNativeJournalFile(ctx, filepath.Join(root, ".native-effects.lock"))
	} else {
		lock, err = lockNativeJournalFileShared(ctx, filepath.Join(root, ".native-effects.lock"))
	}
	if err != nil {
		return nil, errors.Join(err, turnstile.Close())
	}
	if inventory {
		return func() error { return errors.Join(lock.Close(), turnstile.Close()) }, nil
	}
	if err := turnstile.Close(); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return lock.Close, nil
}

func (v *JailerVMM) nativeEffectRoot() (string, error) {
	if v == nil {
		return "", errors.New("native lifecycle: nil receiver")
	}
	if v.chrootBase == "" {
		return "", errors.New("native lifecycle: chrootBase not configured")
	}
	if err := os.MkdirAll(v.chrootBase, 0o750); err != nil {
		return "", err
	}
	if err := checkNativeEffectDirectory(v.chrootBase); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(v.chrootBase)
	if err != nil {
		return "", err
	}
	return filepath.Abs(root)
}

func (v *JailerVMM) beginNativeInventory(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	root, err := v.nativeEffectRoot()
	if err != nil {
		return nil, nil, err
	}
	return v.nativeEffects.begin(ctx, root, true, func(ctx context.Context, inventory bool) (func() error, error) {
		return lockNativeEffects(ctx, root, inventory)
	})
}

func (v *JailerVMM) withNativeInventory(ctx context.Context, observe func(context.Context) error) (result error) {
	ctx, release, err := v.beginNativeInventory(ctx)
	if err != nil {
		return err
	}
	defer func() {
		release()
		b := ctx.Value(nativeEffectContextKey{}).(*nativeEffectPermit).barrier
		b.mu.Lock()
		result = errors.Join(result, b.poison)
		b.mu.Unlock()
	}()
	return observe(ctx)
}

func (v *JailerVMM) checkNativeInventoryPermit(ctx context.Context, p *nativeEffectPermit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || ctx.Value(nativeEffectContextKey{}) != p || p.barrier != &v.nativeEffects {
		return errors.New("native inventory: original inventory context is required")
	}
	p.barrier.mu.Lock()
	defer p.barrier.mu.Unlock()
	if !p.active || !p.inventory || p.family.references <= 0 {
		return errNativeEffectExpired
	}
	return p.barrier.poison
}

func (v *JailerVMM) beginNativeEffects(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	root, err := v.nativeEffectRoot()
	if err != nil {
		return nil, nil, err
	}
	// Native initialization switches the root's ownership mode under the
	// same exclusive lock. A legacy producer already in flight finishes first.
	if r := v.nativeRecovery; r != nil {
		r.mu.Lock()
		owned := r.daemonLock != nil
		lostOwner := !owned && (r.everOwned || len(r.owned) != 0)
		r.mu.Unlock()
		if lostOwner {
			return nil, nil, errNativeDaemonOwnershipLost
		}
		if !owned {
			if err := v.withNativeInventory(ctx, r.acquireDaemonOwnership); err != nil {
				return nil, nil, err
			}
		}
	}
	ctx, release, err := v.nativeEffects.begin(ctx, root, false, func(ctx context.Context, inventory bool) (func() error, error) {
		return lockNativeEffects(ctx, root, inventory)
	})
	if err != nil {
		return nil, nil, err
	}
	if r := v.nativeRecovery; r != nil {
		if err := r.acquireDaemonOwnership(ctx); err != nil {
			release()
			return nil, nil, err
		}
	}
	if err := v.checkNativeRecoveryMode(); err != nil {
		release()
		return nil, nil, fmt.Errorf("native lifecycle: ownership mode: %w", err)
	}
	return ctx, release, nil
}

type nativeEffectVMM interface {
	beginNativeEffects(context.Context) (context.Context, func(), error)
	withNativeInventory(context.Context, func(context.Context) error) error
}

func (m *Manager) beginNativeEffects(ctx context.Context) (context.Context, func(), error) {
	if m == nil {
		return nil, nil, errors.New("native lifecycle: nil manager")
	}
	v, ok := m.vmm.(nativeEffectVMM)
	if !ok {
		if m.nativeVMM() != nil {
			return nil, nil, errors.New("native lifecycle: complete producer barrier is unavailable")
		}
		return ctx, func() {}, nil
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return nil, nil, err
	}
	return v.beginNativeEffects(ctx)
}

// RunNativeHostMaintenance coordinates daemon-owned resource sweeps with
// native inventory. It is an in-process vmmd hook, not an RPC capability.
func (m *Manager) RunNativeHostMaintenance(ctx context.Context, maintain func(context.Context) error) error {
	ctx, release, err := m.beginNativeEffects(ctx)
	if err != nil {
		return err
	}
	defer release()
	return maintain(ctx)
}

// Destroy must still stop and clean an already-owned builder after RPC expiry.
// Admission to the barrier outlives that cancellation; the returned operation
// context retains it so Destroy's wait still selects its cancellation branch.
func (v *JailerVMM) beginNativeCleanup(ctx context.Context) (context.Context, func(), error) {
	permitCtx, release, err := v.beginNativeEffects(context.WithoutCancel(ctx))
	if err != nil {
		return nil, nil, err
	}
	return context.WithValue(ctx, nativeEffectContextKey{}, permitCtx.Value(nativeEffectContextKey{})), release, nil
}
