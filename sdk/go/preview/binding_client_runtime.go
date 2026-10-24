package faas

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"sync"
)

type clientBinding[T any] struct {
	mu               sync.RWMutex
	reloads          chan struct{}
	epoch            uint64
	label, resource  string
	prepare          func(map[string]string) (func(context.Context) (T, string, error), error)
	validate         func(context.Context, T) error
	active           *bindingClient[T]
	retired          map[*bindingClient[T]]struct{}
	building         int
	changed          chan struct{}
	retire           func(T) error
	onRetireError    func(error)
	retirementFailed bool
}

// Reload constructs and optionally tests a candidate in the supplied factory,
// then atomically publishes it. Use the secrets map from Gregale's atomic
// {revision,secrets} envelope, not stale process environment after a rotation.
// ACK that envelope only after Reload succeeds and all other consumers apply.
// Failure after admission disables new acquisition, including revocation.
// Cancellation before admission leaves the current client unchanged.
// Errors deliberately omit configuration, credentials and driver diagnostics.
func (b *clientBinding[T]) Reload(ctx context.Context, values map[string]string) error {
	// Copy before waiting: a queued reload must not borrow a subsequent map.
	snapshot := maps.Clone(values)
	b.mu.RLock()
	epoch := b.epoch
	b.mu.RUnlock()
	if ctx.Err() != nil {
		return b.failure("reload cancelled")
	}
	select {
	case b.reloads <- struct{}{}:
	case <-ctx.Done():
		return b.failure("reload cancelled")
	}
	b.mu.Lock()
	if b.epoch != epoch || ctx.Err() != nil {
		b.mu.Unlock()
		<-b.reloads
		return b.failure("reload cancelled")
	}
	b.building++
	b.notifyLocked()
	b.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		// Keep serialization until even an uncooperative factory finishes, while
		// allowing the caller to cancel and Invalidate/Current to remain prompt.
		defer func() {
			b.mu.Lock()
			b.building--
			b.notifyLocked()
			b.mu.Unlock()
			<-b.reloads
		}()
		done <- b.construct(ctx, snapshot, epoch)
	}()
	select {
	case err := <-done:
		if ctx.Err() == nil {
			return err
		}
	case <-ctx.Done():
	}
	b.mu.Lock()
	if b.epoch == epoch {
		b.invalidateLocked()
	}
	b.mu.Unlock()
	return b.failure("reload cancelled")
}

func (b *clientBinding[T]) construct(ctx context.Context, values map[string]string, epoch uint64) (err error) {
	defer func() {
		if recover() != nil {
			err = b.failure("client could not be configured")
		}
	}()
	b.mu.Lock()
	if b.epoch != epoch {
		b.mu.Unlock()
		return b.failure("reload cancelled")
	}
	b.clearActiveLocked()
	b.mu.Unlock()
	build, err := b.prepare(values)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return b.failure("reload cancelled")
	}
	client, value, err := build(ctx)
	if !validBindingClient(client) {
		if err != nil {
			return b.failure("client could not be configured")
		}
		return b.failure("client is unavailable")
	}
	b.mu.Lock()
	// A reused tracked client still belongs to its earlier leases. Reject the
	// alias without disposing it. Factories must supply fresh exclusive owners.
	if b.retire != nil && b.trackedClientLocked(client) {
		b.mu.Unlock()
		return b.failure("factory reused an owned client")
	}
	candidate := &bindingClient[T]{client: client, bucket: value}
	if err != nil {
		b.retireLocked(candidate)
		b.mu.Unlock()
		return b.failure("client could not be configured")
	}
	if ctx.Err() != nil || b.epoch != epoch {
		b.retireLocked(candidate)
		b.mu.Unlock()
		return b.failure("reload cancelled")
	}
	b.mu.Unlock()
	err = b.validateClient(ctx, client)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.retireLocked(candidate)
		return err
	}
	if ctx.Err() != nil || b.epoch != epoch {
		b.retireLocked(candidate)
		return b.failure("reload cancelled")
	}
	b.active = candidate
	return nil
}

// Invalidate disables new acquisition and fences active/queued candidates.
// It does not wait for factories or retire clients held by outstanding leases.
func (b *clientBinding[T]) Invalidate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.invalidateLocked()
}

func (b *clientBinding[T]) invalidateLocked() {
	b.epoch++
	b.clearActiveLocked()
}

func (b *clientBinding[T]) SecretConsumer() SecretConsumer {
	return SecretConsumer{
		Apply:      func(ctx context.Context, snapshot SecretSnapshot) error { return b.Reload(ctx, snapshot.Secrets()) },
		Invalidate: b.Invalidate,
	}
}

func (b *clientBinding[T]) current() (T, string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.active == nil {
		var zero T
		return zero, "", b.failure("binding is not ready")
	}
	if b.retire != nil {
		var zero T
		return zero, "", b.failure("binding requires lease acquisition for managed retirement")
	}
	return b.active.client, b.active.bucket, nil
}

func validBindingClient[T any](client T) bool {
	v := reflect.ValueOf(client)
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return !v.IsNil()
	default:
		return true
	}
}

func (b *clientBinding[T]) failure(message string) error {
	return errors.New("faas: " + b.label + " " + strings.ReplaceAll(message, "client", b.resource))
}

func (b *clientBinding[T]) validateClient(ctx context.Context, client T) (err error) {
	defer func() {
		if recover() != nil {
			err = b.failure("client validation failed")
		}
	}()
	if b.validate != nil && b.validate(ctx, client) != nil {
		return b.failure("client validation failed")
	}
	return nil
}

type bindingClient[T any] struct {
	client             T
	bucket             string
	leases             uint64
	retired, scheduled bool
}

type bindingLeaseState[T any] struct {
	binding  *clientBinding[T]
	entry    *bindingClient[T]
	released bool // guarded by binding.mu; copies of the public lease share it
}

// bindingLease pins a client until Release. Hold it through uploads,
// downloads and response-body closure; Current after Release fails closed.
// Neither cancelling a retirement wait nor invalidation closes a leased client.
type bindingLease[T any] struct{ state *bindingLeaseState[T] }

func (b *clientBinding[T]) acquire() (*bindingLease[T], error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active == nil {
		return nil, b.failure("binding is not ready")
	}
	b.active.leases++
	return &bindingLease[T]{state: &bindingLeaseState[T]{binding: b, entry: b.active}}, nil
}

func (l *bindingLease[T]) Current() (T, string, error) {
	var zero T
	if l == nil || l.state == nil {
		return zero, "", errors.New("faas: lease is unavailable")
	}
	s := l.state
	s.binding.mu.RLock()
	defer s.binding.mu.RUnlock()
	if s.released {
		return zero, "", s.binding.failure("lease is released")
	}
	return s.entry.client, s.entry.bucket, nil
}

// Release is idempotent, including concurrent calls or copies of this lease.
// It schedules cleanup without waiting for application retirement callbacks.
func (l *bindingLease[T]) Release() {
	if l == nil || l.state == nil {
		return
	}
	s := l.state
	b := s.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.released {
		return
	}
	s.released = true
	s.entry.leases--
	b.scheduleRetirementLocked(s.entry)
}

// WaitForRetirement waits for admitted factories and all retired leases/cleanup.
// It ignores the current client: stop serving/reloading and Invalidate first
// when draining shutdown. Cancellation only ends the wait, never closes leases.
// Any callback failure is sticky and sanitized; uncertain cleanup is not retried.
func (b *clientBinding[T]) WaitForRetirement(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return b.failure("retirement wait cancelled")
		}
		b.mu.RLock()
		if b.building == 0 && len(b.retired) == 0 {
			failed := b.retirementFailed
			b.mu.RUnlock()
			if failed {
				return b.failure("client retirement failed")
			}
			return nil
		}
		changed := b.changed
		b.mu.RUnlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return b.failure("retirement wait cancelled")
		}
	}
}

func (b *clientBinding[T]) notifyLocked() { close(b.changed); b.changed = make(chan struct{}) }

func (b *clientBinding[T]) clearActiveLocked() {
	if b.active != nil {
		entry := b.active
		b.active = nil
		b.retireLocked(entry)
	}
}

func (b *clientBinding[T]) retireLocked(entry *bindingClient[T]) {
	if entry.retired {
		return
	}
	entry.retired = true
	b.retired[entry] = struct{}{}
	b.scheduleRetirementLocked(entry)
	b.notifyLocked()
}

func (b *clientBinding[T]) scheduleRetirementLocked(entry *bindingClient[T]) {
	if !entry.retired || entry.leases != 0 || entry.scheduled {
		return
	}
	entry.scheduled = true
	if b.retire == nil {
		delete(b.retired, entry)
		b.notifyLocked()
		return
	}
	go b.disposeClient(entry)
}

func (b *clientBinding[T]) disposeClient(entry *bindingClient[T]) {
	failed := false
	func() {
		defer func() {
			if recover() != nil {
				failed = true
			}
		}()
		failed = b.retire(entry.client) != nil
	}()
	b.mu.Lock()
	delete(b.retired, entry)
	b.retirementFailed = b.retirementFailed || failed
	b.notifyLocked()
	b.mu.Unlock()
	if failed && b.onRetireError != nil {
		func() {
			defer func() { _ = recover() }()
			b.onRetireError(b.failure("client retirement failed"))
		}()
	}
}

func (b *clientBinding[T]) trackedClientLocked(client T) bool {
	value := reflect.ValueOf(client)
	if !value.Comparable() {
		return false
	} // Fresh exclusive ownership remains the factory contract.
	same := func(entry *bindingClient[T]) bool {
		other := reflect.ValueOf(entry.client)
		return other.IsValid() && other.Type() == value.Type() && value.Interface() == other.Interface()
	}
	if b.active != nil && same(b.active) {
		return true
	}
	for entry := range b.retired {
		if same(entry) {
			return true
		}
	}
	return false
}
