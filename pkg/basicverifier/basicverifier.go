// Package basicverifier makes and checks the verify-only form of an app's
// basic-auth credential (ADR-20261110210000 slice 3h).
//
// Any compute node's edge checks basic auth for any app, so app ownership
// cannot narrow who needs the credential. Instead the edge never opens it:
// apid, which holds the plaintext when the customer sets it, stores an
// argon2id verifier beside the sealed value, keyed by the SHA-256 of the
// sealed blob (app_basic_auth_verifiers). Every path that copies the sealed
// blob (previews, promotions, workload specs) carries the same key, and a
// changed credential is a new blob with a new key, so a verifier can never
// outlive the credential it was made from.
package basicverifier

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parameters: OWASP's argon2id floor (19 MiB, 2 passes, 1 lane). A verify
// runs on the request path only on a cache miss, and the gateway's failed
// guess budget bounds how many a client can force.
const (
	memoryKiB uint32 = 19 * 1024
	passes    uint32 = 2
	lanes     uint8  = 1
	saltLen          = 16
	keyLen    uint32 = 32
	prefix           = "$argon2id$"
)

// MaxConcurrent bounds the derivations in flight in one process: each holds
// 19 MiB.
const MaxConcurrent = 4

var slots = make(chan struct{}, MaxConcurrent)

// ErrMalformed is a verifier string this package did not make.
var ErrMalformed = errors.New("basicverifier: malformed verifier")

// Key is the lookup key of a sealed credential: the SHA-256 of the blob.
func Key(sealed []byte) []byte {
	sum := sha256.Sum256(sealed)
	return sum[:]
}

// material is what is hashed: the sealed plaintext's own layout.
func material(user, pass string) []byte {
	return []byte(user + "\n" + pass)
}

// New returns the argon2id verifier (PHC string) of user and pass.
func New(ctx context.Context, user, pass string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("basicverifier: salt: %w", err)
	}
	hash, err := derive(ctx, material(user, pass), salt, passes, memoryKiB, lanes, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memoryKiB, passes, lanes,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

// Verify reports whether user and pass match verifier, comparing in
// constant time. The parameters come from the verifier string, bounded so a
// stored value cannot ask for unbounded memory.
func Verify(ctx context.Context, verifier, user, pass string) (bool, error) {
	mem, t, p, salt, want, err := parse(verifier)
	if err != nil {
		return false, err
	}
	got, err := derive(ctx, material(user, pass), salt, t, mem, p, uint32(len(want))) //nolint:gosec // len(want) is bounded by parse
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func derive(ctx context.Context, password, salt []byte, t, mem uint32, p uint8, n uint32) ([]byte, error) {
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-slots }()
	return argon2.IDKey(password, salt, t, mem, p, n), nil
}

func parse(verifier string) (mem, t uint32, p uint8, salt, hash []byte, err error) {
	if !strings.HasPrefix(verifier, prefix) {
		return 0, 0, 0, nil, nil, ErrMalformed
	}
	parts := strings.Split(verifier, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash
	if len(parts) != 6 {
		return 0, 0, 0, nil, nil, ErrMalformed
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, ErrMalformed
	}
	var lanesRead uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &lanesRead); err != nil {
		return 0, 0, 0, nil, nil, ErrMalformed
	}
	if mem < 8*1024 || mem > 64*1024 || t < 1 || t > 4 || lanesRead < 1 || lanesRead > 4 {
		return 0, 0, 0, nil, nil, ErrMalformed
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return 0, 0, 0, nil, nil, ErrMalformed
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) < 16 || len(hash) > 64 {
		return 0, 0, 0, nil, nil, ErrMalformed
	}
	return mem, t, uint8(lanesRead), salt, hash, nil
}
