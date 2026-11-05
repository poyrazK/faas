package state

import (
	"bytes"
	"crypto/subtle"
)

// IdempotencyFingerprintSize is the length of a request fingerprint
// (ADR-20261022204036), the size the idempotency_keys.request_digest CHECK
// allows.
const IdempotencyFingerprintSize = 32

// IdempotencyFingerprintKeyIDSize is the length of the key id that starts
// every request fingerprint. Fingerprints made under different keys are not
// compared: after a key rotation, or between replicas holding different
// keys, a stored key is unbound and replays as it did before fingerprints.
const IdempotencyFingerprintKeyIDSize = 4

// idempotencyFingerprintsConflict reports whether a request fingerprint
// differs from the one its key is bound to, made under the same key.
func idempotencyFingerprintsConflict(stored, fingerprint []byte) bool {
	return len(stored) == IdempotencyFingerprintSize && len(fingerprint) == IdempotencyFingerprintSize &&
		bytes.Equal(stored[:IdempotencyFingerprintKeyIDSize], fingerprint[:IdempotencyFingerprintKeyIDSize]) &&
		subtle.ConstantTimeCompare(stored, fingerprint) != 1
}
