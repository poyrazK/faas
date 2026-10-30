package productemail

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// unsubscribeColumn is the column unsubscribe digests are bound to. Once the
// account policy's keyed writes are on, an unsubscribe token's hash is
// stored as a keyed digest (ADR 20261029202403).
const unsubscribeColumn credhash.Tag = "product_control_email_deliveries.unsubscribe_hash"

// storedUnsubscribeHash is the value an unsubscribe token whose SHA-256 is
// sum is written as.
func storedUnsubscribeHash(sum []byte) []byte {
	return credhash.Account().Store(unsubscribeColumn, sum)
}

// findUnsubscribe runs find with each value an unsubscribe token whose
// SHA-256 is sum may be stored as and returns the first result and the
// value that found it; ErrNotFound when none does.
func findUnsubscribe[T any](sum []byte, find func([]byte) (T, error)) (T, []byte, error) {
	var zero T
	for _, value := range credhash.Account().Lookups(unsubscribeColumn, sum) {
		out, err := find(value)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return zero, nil, err
		}
		return out, value, nil
	}
	return zero, nil, ErrNotFound
}
