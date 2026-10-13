package faas

import (
	"context"
	"errors"
	"reflect"
)

// ObjectStorageBindingOptions opts into SDK-owned retirement. Each factory
// result must exclusively own fresh resources; never share an underlying pool
// with another candidate. Retire runs off the state lock, may run concurrently
// for different clients, and must bound its own work. It is invoked once,
// without retries. OnRetireError receives only sanitized errors, outside locks.
type ObjectStorageBindingOptions[T any] struct {
	Retire        func(T) error
	OnRetireError func(error)
}

type objectStorageClient[T any] struct {
	client             T
	bucket             string
	leases             uint64
	retired, scheduled bool
}

type objectStorageLeaseState[T any] struct {
	binding  *ObjectStorageBinding[T]
	entry    *objectStorageClient[T]
	released bool // guarded by binding.mu; copies of the public lease share it
}

// ObjectStorageLease pins a client until Release. Hold it through uploads,
// downloads and response-body closure; Current after Release fails closed.
// Neither cancelling a retirement wait nor invalidation closes a leased client.
type ObjectStorageLease[T any] struct{ state *objectStorageLeaseState[T] }

func (ObjectStorageLease[T]) String() string   { return "ObjectStorageLease{redacted}" }
func (ObjectStorageLease[T]) GoString() string { return "ObjectStorageLease{redacted}" }
func (ObjectStorageLease[T]) MarshalJSON() ([]byte, error) {
	return []byte(`"ObjectStorageLease{redacted}"`), nil
}

func (b *ObjectStorageBinding[T]) Acquire() (*ObjectStorageLease[T], error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active == nil {
		return nil, errors.New("faas: object-storage binding is not ready")
	}
	b.active.leases++
	return &ObjectStorageLease[T]{state: &objectStorageLeaseState[T]{binding: b, entry: b.active}}, nil
}

func (l *ObjectStorageLease[T]) Current() (T, string, error) {
	var zero T
	if l == nil || l.state == nil {
		return zero, "", errors.New("faas: object-storage lease is unavailable")
	}
	s := l.state
	s.binding.mu.RLock()
	defer s.binding.mu.RUnlock()
	if s.released {
		return zero, "", errors.New("faas: object-storage lease is released")
	}
	return s.entry.client, s.entry.bucket, nil
}

// Release is idempotent, including concurrent calls or copies of this lease.
// It schedules cleanup without waiting for application retirement callbacks.
func (l *ObjectStorageLease[T]) Release() {
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
func (b *ObjectStorageBinding[T]) WaitForRetirement(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return errors.New("faas: object-storage retirement wait cancelled")
		}
		b.mu.RLock()
		if b.building == 0 && len(b.retired) == 0 {
			failed := b.retirementFailed
			b.mu.RUnlock()
			if failed {
				return errors.New("faas: object-storage client retirement failed")
			}
			return nil
		}
		changed := b.changed
		b.mu.RUnlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return errors.New("faas: object-storage retirement wait cancelled")
		}
	}
}

func (b *ObjectStorageBinding[T]) notifyLocked() { close(b.changed); b.changed = make(chan struct{}) }

func (b *ObjectStorageBinding[T]) clearActiveLocked() {
	if b.active != nil {
		entry := b.active
		b.active = nil
		b.retireLocked(entry)
	}
}

func (b *ObjectStorageBinding[T]) retireLocked(entry *objectStorageClient[T]) {
	if entry.retired {
		return
	}
	entry.retired = true
	b.retired[entry] = struct{}{}
	b.scheduleRetirementLocked(entry)
	b.notifyLocked()
}

func (b *ObjectStorageBinding[T]) scheduleRetirementLocked(entry *objectStorageClient[T]) {
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

func (b *ObjectStorageBinding[T]) disposeClient(entry *objectStorageClient[T]) {
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
			b.onRetireError(errors.New("faas: object-storage client retirement failed"))
		}()
	}
}

func (b *ObjectStorageBinding[T]) trackedClientLocked(client T) bool {
	value := reflect.ValueOf(client)
	if !value.Comparable() {
		return false
	} // Fresh exclusive ownership remains the factory contract.
	same := func(entry *objectStorageClient[T]) bool {
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
