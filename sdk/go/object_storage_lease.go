package faas

import "errors"

// ObjectStorageBindingOptions opts into SDK-owned retirement. Each factory
// result must exclusively own fresh resources; never share an underlying pool
// with another candidate. Retire runs off the state lock, may run concurrently
// for different clients, and must bound its own work. It is invoked once,
// without retries. OnRetireError receives only sanitized errors, outside locks.
type ObjectStorageBindingOptions[T any] struct {
	Retire        func(T) error
	OnRetireError func(error)
}

// ObjectStorageLease pins a client through stream consumption/closure.
// Release is idempotent, including shallow copies. Invalidation and cancelled
// retirement waits never close leased resources.
type ObjectStorageLease[T any] struct{ lease *bindingLease[T] }

func (ObjectStorageLease[T]) String() string   { return "ObjectStorageLease{redacted}" }
func (ObjectStorageLease[T]) GoString() string { return "ObjectStorageLease{redacted}" }
func (ObjectStorageLease[T]) MarshalJSON() ([]byte, error) {
	return []byte(`"ObjectStorageLease{redacted}"`), nil
}
func (l *ObjectStorageLease[T]) Current() (T, string, error) {
	if l == nil || l.lease == nil {
		var zero T
		return zero, "", errors.New("faas: object-storage lease is unavailable")
	}
	return l.lease.Current()
}
func (l *ObjectStorageLease[T]) Release() {
	if l != nil && l.lease != nil {
		l.lease.Release()
	}
}
