// Package credhash keys stored credential hashes with a secret the database
// never holds (ADR 20261029202403). A plain SHA-256 of a bearer token lets
// anyone who can write the database insert a token of their own; a keyed
// digest does not.
//
// A digest is computed from the token's SHA-256, not the token, so stored
// hashes can be keyed without their plaintext and hashes that clients compute
// keep working.
package credhash

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	// KeySize is the length of a ring key.
	KeySize = 32
	// DigestSize is the length of a keyed digest: the key ID and the MAC.
	DigestSize = 1 + sha256.Size
	// PlainSize is the length of a plain SHA-256, which a keyed digest
	// replaces.
	PlainSize = sha256.Size

	digestContext = "gregale-credential-v1"
)

// Tag names the column a digest is stored in, so a digest valid in one table
// is not valid in another.
type Tag string

// Ring is a family's keys. The first is current: new digests use it. Older
// keys still verify digests written under them until those are rewritten.
type Ring struct {
	current byte
	keys    map[byte][]byte
	order   []byte
}

var (
	// ErrNoRing is a keyed write configured without a key ring.
	ErrNoRing = errors.New("credhash: keyed writes need a key ring")
)

// ParseRing reads one key per line, `kid:base64(key)`, the current key
// first. kid is 1..255; blank lines and lines starting with # are skipped.
func ParseRing(data []byte) (*Ring, error) {
	r := &Ring{keys: map[byte][]byte{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		id, encoded, ok := strings.Cut(text, ":")
		if !ok {
			return nil, fmt.Errorf("credhash: ring line %d is not kid:key", line)
		}
		n, err := strconv.Atoi(id)
		if err != nil || n < 1 || n > 255 {
			return nil, fmt.Errorf("credhash: ring line %d: key ID must be 1..255", line)
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != KeySize {
			return nil, fmt.Errorf("credhash: ring line %d: key must be %d base64 bytes", line, KeySize)
		}
		kid := byte(n)
		if _, dup := r.keys[kid]; dup {
			return nil, fmt.Errorf("credhash: ring line %d: key ID %d repeats", line, n)
		}
		if len(r.order) == 0 {
			r.current = kid
		}
		r.keys[kid] = key
		r.order = append(r.order, kid)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("credhash: read ring: %w", err)
	}
	if len(r.order) == 0 {
		return nil, errors.New("credhash: the ring holds no key")
	}
	return r, nil
}

// LoadRing reads a ring file.
func LoadRing(path string) (*Ring, error) {
	data, err := os.ReadFile(path) //nolint:forbidigo // operator-provisioned credential named by configuration
	if err != nil {
		return nil, fmt.Errorf("credhash: read ring: %w", err)
	}
	return ParseRing(data)
}

// NewKeyLine returns a ring line holding a new random key under kid.
func NewKeyLine(kid int) (string, error) {
	if kid < 1 || kid > 255 {
		return "", errors.New("credhash: key ID must be 1..255")
	}
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%s", kid, base64.StdEncoding.EncodeToString(key)), nil
}

// NextKeyID is the ID a rotation's new key takes: one above the highest in
// the ring, or the lowest free ID once 255 is taken.
func (r *Ring) NextKeyID() (int, error) {
	highest := 0
	for _, kid := range r.order {
		highest = max(highest, int(kid))
	}
	if highest < 255 {
		return highest + 1, nil
	}
	for kid := 1; kid <= 255; kid++ {
		if _, used := r.keys[byte(kid)]; !used {
			return kid, nil
		}
	}
	return 0, errors.New("credhash: every key ID is in use")
}

// Current is the ID of the key new digests use.
func (r *Ring) Current() byte { return r.current }

func (r *Ring) mac(kid byte, tag Tag, sum []byte) []byte {
	m := hmac.New(sha256.New, r.keys[kid])
	for _, part := range [][]byte{[]byte(digestContext), []byte(tag), sum} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(part))) //nolint:gosec // parts are short constants and hashes
		m.Write(n[:])
		m.Write(part)
	}
	return append([]byte{kid}, m.Sum(nil)...)
}

// Digest is the keyed digest of sum, a token's SHA-256, under the current
// key.
func (r *Ring) Digest(tag Tag, sum []byte) []byte { return r.mac(r.current, tag, sum) }

// Digests are sum's keyed digests under every key in the ring, the current
// key's first.
func (r *Ring) Digests(tag Tag, sum []byte) [][]byte {
	out := make([][]byte, 0, len(r.order))
	for _, kid := range r.order {
		out = append(out, r.mac(kid, tag, sum))
	}
	return out
}

// Keyed reports whether a stored value is a keyed digest, by its length.
func Keyed(stored []byte) bool { return len(stored) == DigestSize }

// Policy is how one process writes and looks up a family's credentials
// during the rollout (ADR 20261029202403, decision 4). The nil Policy is the
// release before it: plain writes, plain lookups.
type Policy struct {
	// Ring is the family's keys; nil looks up and writes plain hashes only.
	Ring *Ring
	// KeyedWrites stores keyed digests. Turn it on only once every reader
	// runs a release that looks keyed digests up.
	KeyedWrites bool
	// PlainLookup still accepts a plain hash. Off is the contract step.
	PlainLookup bool
}

// Validate refuses a policy that cannot hold: keyed writes without a ring,
// or neither keyed nor plain lookups.
func (p *Policy) Validate() error {
	if p == nil {
		return nil
	}
	if p.KeyedWrites && p.Ring == nil {
		return ErrNoRing
	}
	if !p.PlainLookup && p.Ring == nil {
		return errors.New("credhash: turning plain lookup off needs a key ring")
	}
	return nil
}

// Store is the value to write for a credential whose SHA-256 is sum.
func (p *Policy) Store(tag Tag, sum []byte) []byte {
	if p == nil || !p.KeyedWrites || p.Ring == nil {
		return bytes.Clone(sum)
	}
	return p.Ring.Digest(tag, sum)
}

// Lookups are the stored values a credential whose SHA-256 is sum may hold,
// in the order to try them: keyed under each key, then plain while plain
// lookup is on.
func (p *Policy) Lookups(tag Tag, sum []byte) [][]byte {
	if p == nil {
		return [][]byte{bytes.Clone(sum)}
	}
	var out [][]byte
	if p.Ring != nil {
		out = p.Ring.Digests(tag, sum)
	}
	if p.PlainLookup || p.Ring == nil {
		out = append(out, bytes.Clone(sum))
	}
	return out
}

// Matches reports, in constant time per candidate, whether stored is the
// value a credential whose SHA-256 is sum may hold. A caller that loads a
// row by another key (an ID, a prefix) and then checks the token uses it.
func (p *Policy) Matches(tag Tag, sum, stored []byte) bool {
	if len(sum) != PlainSize || len(stored) == 0 {
		return false
	}
	matched := false
	for _, candidate := range p.Lookups(tag, sum) {
		if hmac.Equal(candidate, stored) {
			matched = true
		}
	}
	return matched
}

// Stale reports whether a stored value found for sum should be rewritten:
// keyed writes are on and it is plain or under an older key.
func (p *Policy) Stale(stored []byte) bool {
	if p == nil || !p.KeyedWrites || p.Ring == nil {
		return false
	}
	return !Keyed(stored) || stored[0] != p.Ring.current
}

// Derive is a capability derived from material (amendment 1, decision 3):
// the hex HMAC of material under the current key once keyed writes are on,
// legacy, the old derivation, before. A derived capability keeps the old
// one's shape, 64 hex characters, so the guests that carry it do not change.
func (p *Policy) Derive(tag Tag, material []byte, legacy string) string {
	if p == nil || !p.KeyedWrites || p.Ring == nil {
		return legacy
	}
	return hex.EncodeToString(p.Ring.mac(p.Ring.current, tag, material)[1:])
}

// DerivedMatches reports, in constant time per candidate, whether presented
// is a capability p accepts for material: its HMAC under any key in the
// ring, or legacy while plain lookup is on.
func (p *Policy) DerivedMatches(tag Tag, material []byte, legacy, presented string) bool {
	if presented == "" {
		return false
	}
	matched := false
	if p != nil && p.Ring != nil {
		for _, kid := range p.Ring.order {
			if hmac.Equal([]byte(hex.EncodeToString(p.Ring.mac(kid, tag, material)[1:])), []byte(presented)) {
				matched = true
			}
		}
	}
	if p == nil || p.PlainLookup || p.Ring == nil {
		if hmac.Equal([]byte(legacy), []byte(presented)) {
			matched = true
		}
	}
	return matched
}

// HexAll returns the lowercase hex lookups for a column that stores hex text.
func HexAll(values [][]byte) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fmt.Sprintf("%x", v)
	}
	return out
}

// Family names a token family. Each family has its own ring, held only by
// the daemons that issue and check its tokens (decision 2), so a leaked key
// mints one family.
type Family string

// The token families (ADR 20261029202403 decision 2 and amendment 1).
const (
	// FamilyAccount is apid's credentials: API, deploy and consumer keys,
	// mailed tokens, invitations, approvals and apid's own receipts.
	FamilyAccount Family = "account"
	// FamilyScheduler is schedd's work credentials: event acknowledgements,
	// consumer-group leases and CI and operation capabilities. schedd and
	// apid hold it.
	FamilyScheduler Family = "scheduler"
	// FamilyOutbound is outboundd's integration and AI access tokens.
	// outboundd and apid hold it.
	FamilyOutbound Family = "outbound"
	// FamilyUploads is agentd's object upload grants, which s3-gatewayd
	// checks.
	FamilyUploads Family = "uploads"
	// FamilyAttach is the one-shot interactive attach tokens apid issues
	// and vmmd checks: the only family a compute node holds.
	FamilyAttach Family = "attach"
	// FamilyDevBridge is dev-bridge attach and request tokens. apid and
	// bridged hold it; gatewayd-internal asks apid.
	FamilyDevBridge Family = "dev-bridge"
	// FamilyDataAPI is Data API server keys. apid holds it; realtimed asks
	// apid.
	FamilyDataAPI Family = "data-api"
)

var (
	activeMu sync.RWMutex
	active   = map[Family]*Policy{}
)

// Set installs family's policy for this process; nil restores plain
// hashes. A daemon installs the policies of the families it issues or
// checks at start.
func Set(family Family, p *Policy) {
	activeMu.Lock()
	defer activeMu.Unlock()
	if p == nil {
		delete(active, family)
		return
	}
	active[family] = p
}

// For is family's policy in this process, nil when none was installed.
func For(family Family) *Policy {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return active[family]
}

// SetAccount installs the account family's policy for this process; nil
// restores plain hashes. apid installs it at start.
func SetAccount(p *Policy) { Set(FamilyAccount, p) }

// Account is the account family's policy in this process, nil when none was
// installed.
func Account() *Policy { return For(FamilyAccount) }
