package apidgrpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/credhash"
)

// MatchCredential asks apid whether stored, read from column, is the value
// a token whose SHA-256 is sum may hold (ADR 20261029202403).
func (c *AuthClientImpl) MatchCredential(ctx context.Context, column string, stored, sum []byte) (bool, error) {
	resp, err := c.cli.MatchCredential(ctx, &apidpb.MatchCredentialRequest{Column: column, Stored: stored, Sha256: sum})
	if err != nil {
		return false, fmt.Errorf("apidgrpc: MatchCredential: %w", err)
	}
	return resp.GetMatch(), nil
}

// credentialMatchClient is the RPC a CredentialMatcher wraps.
type credentialMatchClient interface {
	MatchCredential(ctx context.Context, column string, stored, sum []byte) (bool, error)
}

// credentialMatchCacheCap bounds the matches a gateway remembers; the set
// is cleared when it fills.
const credentialMatchCacheCap = 1 << 16

// CredentialMatcher checks a presented token against a stored credential
// hash through apid, and remembers the matches: a match depends only on
// the stored value and the token, so it holds until the row changes, and
// a changed row has a different stored value. Misses are not remembered;
// callers already budget failed lookups per source.
type CredentialMatcher struct {
	client credentialMatchClient

	mu      sync.Mutex
	matched map[[sha256.Size]byte]struct{}
}

// NewCredentialMatcher wraps an apid auth client.
func NewCredentialMatcher(client credentialMatchClient) *CredentialMatcher {
	return &CredentialMatcher{client: client, matched: map[[sha256.Size]byte]struct{}{}}
}

func matchKey(column string, stored, sum []byte) [sha256.Size]byte {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(column), stored, sum} {
		_, _ = fmt.Fprintf(h, "%d:", len(part))
		_, _ = h.Write(part)
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Match reports whether stored is the value the token whose SHA-256 is sum
// may hold.
func (m *CredentialMatcher) Match(ctx context.Context, column string, stored, sum []byte) (bool, error) {
	if len(stored) == 0 || len(sum) != sha256.Size {
		return false, nil
	}
	key := matchKey(column, stored, sum)
	m.mu.Lock()
	_, ok := m.matched[key]
	m.mu.Unlock()
	if ok {
		return true, nil
	}
	match, err := m.client.MatchCredential(ctx, column, bytes.Clone(stored), sum)
	if err != nil || !match {
		return false, err
	}
	m.mu.Lock()
	if len(m.matched) >= credentialMatchCacheCap {
		clear(m.matched)
	}
	m.matched[key] = struct{}{}
	m.mu.Unlock()
	return true, nil
}

// Remote adapts m to credhash.Remote, for a daemon that holds no ring for a
// family and asks apid instead (ADR 20261029202403 amendment 1).
func (m *CredentialMatcher) Remote() credhash.Remote {
	return func(ctx context.Context, tag credhash.Tag, stored, sum []byte) (bool, error) {
		return m.Match(ctx, string(tag), stored, sum)
	}
}
