package credhash_test

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/credhash"
)

func ring(t *testing.T, kids ...int) *credhash.Ring {
	t.Helper()
	var lines []string
	for _, kid := range kids {
		line, err := credhash.NewKeyLine(kid)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	r, err := credhash.ParseRing([]byte(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// adr: 20261029202403 — a stored credential is a keyed digest a database
// writer cannot compute, bound to its column.
func TestDigestIsKeyedAndBoundToItsColumn(t *testing.T) {
	sum := sha256.Sum256([]byte("fp_token"))
	r := ring(t, 3)
	d := r.Digest("api_keys.key_sha256", sum[:])
	if len(d) != credhash.DigestSize || d[0] != 3 || !credhash.Keyed(d) {
		t.Fatalf("digest = %x", d)
	}
	if bytes.Equal(d, r.Digest("deploy_tokens.token_sha256", sum[:])) {
		t.Fatal("a digest is valid in another column")
	}
	if bytes.Equal(d, ring(t, 3).Digest("api_keys.key_sha256", sum[:])) {
		t.Fatal("two keys made the same digest")
	}
	if credhash.Keyed(sum[:]) {
		t.Fatal("a plain hash reads as keyed")
	}
}

func TestPolicyFollowsTheRollout(t *testing.T) {
	sum := sha256.Sum256([]byte("fp_token"))
	const tag credhash.Tag = "api_keys.key_sha256"
	r := ring(t, 2, 1) // 2 current, 1 previous

	var none *credhash.Policy
	if got := none.Store(tag, sum[:]); !bytes.Equal(got, sum[:]) {
		t.Fatal("the release before writes a plain hash")
	}
	expand := &credhash.Policy{Ring: r, PlainLookup: true}
	if got := expand.Store(tag, sum[:]); !bytes.Equal(got, sum[:]) {
		t.Fatal("expand keeps writing plain hashes until keyed writes are on")
	}
	lookups := expand.Lookups(tag, sum[:])
	if len(lookups) != 3 || lookups[0][0] != 2 || lookups[1][0] != 1 || !bytes.Equal(lookups[2], sum[:]) {
		t.Fatalf("expand lookups = %x", lookups)
	}
	keyed := &credhash.Policy{Ring: r, KeyedWrites: true, PlainLookup: true}
	if got := keyed.Store(tag, sum[:]); got[0] != 2 || !credhash.Keyed(got) {
		t.Fatalf("keyed write = %x", got)
	}
	if !keyed.Stale(sum[:]) || !keyed.Stale(lookups[1]) || keyed.Stale(lookups[0]) {
		t.Fatal("plain and older-key values are rewritten, current ones are not")
	}
	contract := &credhash.Policy{Ring: r, KeyedWrites: true}
	for _, v := range contract.Lookups(tag, sum[:]) {
		if bytes.Equal(v, sum[:]) {
			t.Fatal("the contract step still looks a plain hash up")
		}
	}
	for _, bad := range []*credhash.Policy{{KeyedWrites: true}, {PlainLookup: false}} {
		if bad.Validate() == nil {
			t.Fatalf("policy %+v validated", bad)
		}
	}
}

// adr: 20261029202403 amendment 1 decision 3 — a derived capability is an
// HMAC under the family key once keyed writes are on, keeps the old shape,
// and the old derivation is accepted only while plain lookup is on.
func TestDerivedCapabilitiesFollowTheRollout(t *testing.T) {
	const tag credhash.Tag = "ci_job_runtime.capability"
	material := []byte("run\x00instance\x00lease")
	legacy := strings.Repeat("ab", 32)
	r := ring(t, 2, 1)

	var none *credhash.Policy
	if got := none.Derive(tag, material, legacy); got != legacy || !none.DerivedMatches(tag, material, legacy, legacy) {
		t.Fatal("the release before derives and accepts the old capability")
	}
	expand := &credhash.Policy{Ring: r, PlainLookup: true}
	if got := expand.Derive(tag, material, legacy); got != legacy {
		t.Fatal("expand keeps issuing the old capability")
	}
	keyed := &credhash.Policy{Ring: r, KeyedWrites: true, PlainLookup: true}
	derived := keyed.Derive(tag, material, legacy)
	if derived == legacy || len(derived) != 64 || strings.ToLower(derived) != derived {
		t.Fatalf("keyed capability = %q", derived)
	}
	for _, p := range []*credhash.Policy{expand, keyed} {
		if !p.DerivedMatches(tag, material, legacy, derived) || !p.DerivedMatches(tag, material, legacy, legacy) {
			t.Fatal("the rollout refuses a capability it issues")
		}
	}
	older := (&credhash.Policy{Ring: ring(t, 9), KeyedWrites: true}).Derive(tag, material, legacy)
	contract := &credhash.Policy{Ring: r, KeyedWrites: true}
	switch {
	case !contract.DerivedMatches(tag, material, legacy, derived):
		t.Fatal("contract refuses the keyed capability")
	case contract.DerivedMatches(tag, material, legacy, legacy):
		t.Fatal("contract still accepts the old derivation")
	case contract.DerivedMatches(tag, material, legacy, older):
		t.Fatal("a key outside the ring was accepted")
	case contract.DerivedMatches(tag, []byte("run\x00instance\x00other"), legacy, derived):
		t.Fatal("a capability for other material was accepted")
	case contract.DerivedMatches("job_operation.capability", material, legacy, derived):
		t.Fatal("a capability for another tag was accepted")
	}
}

func TestParseRingRefusesBadLines(t *testing.T) {
	good, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"", "# only a comment", "1", "0:" + strings.Split(good, ":")[1], "1:short", good + "\n" + good} {
		if _, err := credhash.ParseRing([]byte(data)); err == nil {
			t.Fatalf("ring %q parsed", data)
		}
	}
}
