package sealsig

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Runtime secrets (ADR 20261028161502, amendment 1). apid signs each runtime
// envelope it seals and stores the signature by the envelope's SHA-256 in
// runtime_secret_signatures; vmmd looks the signatures up and verifies them
// before it opens the envelopes.

// RuntimeStore keeps runtime secret signatures by envelope digest.
// *state.PgStore implements it.
type RuntimeStore interface {
	PutRuntimeSecretSignature(ctx context.Context, digest, signature []byte) error
	RuntimeSecretSignatures(ctx context.Context, digests [][]byte) (map[string][]byte, error)
}

// RuntimeLookup is the read half of RuntimeStore, all vmmd needs.
type RuntimeLookup interface {
	RuntimeSecretSignatures(ctx context.Context, digests [][]byte) (map[string][]byte, error)
}

// RuntimeDigest is the key a runtime envelope's signature is stored under.
func RuntimeDigest(blob []byte) []byte {
	sum := sha256.Sum256(blob)
	return sum[:]
}

// SignRuntime signs a runtime envelope and stores its signature.
func SignRuntime(ctx context.Context, store RuntimeStore, signer *Signer, blob []byte) error {
	if err := store.PutRuntimeSecretSignature(ctx, RuntimeDigest(blob), signer.Sign(RuntimeSecretMessage(blob))); err != nil {
		return fmt.Errorf("sealsig: store runtime secret signature: %w", err)
	}
	return nil
}

// runtimeSignTimeout bounds the signature write a seal waits for.
const runtimeSignTimeout = 10 * time.Second

// RuntimeSealHook returns the hook a writer installs with
// secretbox.SetRuntimeSealHook: every runtime envelope it seals is signed,
// and the signature stored, before the seal returns. secretbox seals carry
// no context, so each write runs under ctx, the writer's lifetime, with its
// own timeout.
func RuntimeSealHook(ctx context.Context, store RuntimeStore, signer *Signer) func([]byte) error {
	return func(blob []byte) error {
		write, cancel := context.WithTimeout(ctx, runtimeSignTimeout)
		defer cancel()
		return SignRuntime(write, store, signer, blob)
	}
}

// BlobError names which of the envelopes passed to RuntimeChecker.Check was
// refused.
type BlobError struct {
	Index int
	Err   error
}

func (e *BlobError) Error() string { return e.Err.Error() }
func (e *BlobError) Unwrap() error { return e.Err }

const (
	// runtimeLookupTimeout bounds one signature lookup on the wake path.
	runtimeLookupTimeout = 5 * time.Second
	// runtimeLookupBatch matches state.MaxRuntimeSecretSignatureLookup.
	runtimeLookupBatch = 4096
	// runtimeVerifiedCap bounds the digests a checker remembers; the set is
	// cleared when it fills.
	runtimeVerifiedCap = 1 << 16
)

// RuntimeChecker verifies runtime envelopes' signatures before vmmd opens
// them. It remembers the digests it verified, so a later wake of the same
// values does not query again.
type RuntimeChecker struct {
	lookup   RuntimeLookup
	verifier *Verifier
	log      *slog.Logger

	mu       sync.Mutex
	verified map[[sha256.Size]byte]struct{}
	// unsigned holds the unsigned digests already logged, so each is
	// logged once rather than on every wake.
	unsigned map[[sha256.Size]byte]struct{}
}

// NewRuntimeChecker checks envelopes against verifier with signatures read
// from lookup.
func NewRuntimeChecker(lookup RuntimeLookup, verifier *Verifier, log *slog.Logger) (*RuntimeChecker, error) {
	if lookup == nil || verifier == nil {
		return nil, errors.New("sealsig: runtime checker needs a signature lookup and a verifier")
	}
	if log == nil {
		log = slog.Default()
	}
	return &RuntimeChecker{lookup: lookup, verifier: verifier, log: log, verified: map[[sha256.Size]byte]struct{}{},
		unsigned: map[[sha256.Size]byte]struct{}{}}, nil
}

// Check reports whether every blob may be opened. A refused blob is a
// *BlobError naming its index. A nil checker checks nothing. Before the
// contract step a failed lookup counts as unsigned, which passes; after it
// the lookup error fails the check.
func (c *RuntimeChecker) Check(ctx context.Context, blobs [][]byte) error {
	if c == nil || len(blobs) == 0 {
		return nil
	}
	digests := make([][sha256.Size]byte, len(blobs))
	var missing [][]byte
	queued := map[[sha256.Size]byte]bool{}
	c.mu.Lock()
	for i, blob := range blobs {
		digests[i] = sha256.Sum256(blob)
		if _, ok := c.verified[digests[i]]; !ok && !queued[digests[i]] {
			queued[digests[i]] = true
			missing = append(missing, digests[i][:])
		}
	}
	c.mu.Unlock()
	if len(missing) == 0 {
		return nil
	}
	found, err := c.fetch(ctx, missing)
	if err != nil {
		if c.verifier.Require {
			return fmt.Errorf("sealsig: look up runtime secret signatures: %w", err)
		}
		c.log.Warn("sealsig: runtime secret signature lookup failed; serving the values as unsigned", "err", err)
		found = map[string][]byte{}
	}
	var good [][sha256.Size]byte
	for i, blob := range blobs {
		if !queued[digests[i]] {
			continue
		}
		sig := found[string(digests[i][:])]
		if err := c.verifier.Check(RuntimeSecretMessage(blob), sig); err != nil {
			return &BlobError{Index: i, Err: err}
		}
		if len(sig) == 0 {
			c.noteUnsigned(digests[i])
			continue
		}
		good = append(good, digests[i])
	}
	c.remember(good)
	return nil
}

func (c *RuntimeChecker) fetch(ctx context.Context, digests [][]byte) (map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, runtimeLookupTimeout)
	defer cancel()
	found := make(map[string][]byte, len(digests))
	for start := 0; start < len(digests); start += runtimeLookupBatch {
		page, err := c.lookup.RuntimeSecretSignatures(ctx, digests[start:min(start+runtimeLookupBatch, len(digests))])
		if err != nil {
			return nil, err
		}
		for digest, sig := range page {
			found[digest] = sig
		}
	}
	return found, nil
}

func (c *RuntimeChecker) noteUnsigned(digest [sha256.Size]byte) {
	c.mu.Lock()
	_, logged := c.unsigned[digest]
	if !logged {
		if len(c.unsigned) >= runtimeVerifiedCap {
			clear(c.unsigned)
		}
		c.unsigned[digest] = struct{}{}
	}
	c.mu.Unlock()
	if !logged {
		c.log.Warn("sealsig: serving an unsigned runtime secret", "digest", fmt.Sprintf("%x", digest[:8]))
	}
}

func (c *RuntimeChecker) remember(digests [][sha256.Size]byte) {
	if len(digests) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.verified)+len(digests) > runtimeVerifiedCap {
		clear(c.verified)
	}
	for _, digest := range digests {
		c.verified[digest] = struct{}{}
	}
}
