package outbound

import (
	"crypto/sha256"
	"testing"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// adr: 20261029202403 amendment 1 — an integration read from the database
// carries its token hash as stored, plain or keyed under the outbound ring;
// a static integration keeps its configured hash.
func TestValidTokenChecksTheStoredForm(t *testing.T) {
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	credhash.Set(credhash.FamilyOutbound, &credhash.Policy{Ring: ring, KeyedWrites: true})
	t.Cleanup(func() { credhash.Set(credhash.FamilyOutbound, nil) })

	sum := sha256.Sum256([]byte("operator-token"))
	keyed := credhash.For(credhash.FamilyOutbound).Store(IntegrationTokenColumn, sum[:])
	stored := Integration{StoredTokenHash: keyed}
	if !validToken("operator-token", stored) || validToken("other-token", stored) {
		t.Fatal("keyed stored hash not checked against the token")
	}
	if validToken("operator-token", Integration{StoredTokenHash: sum[:]}) {
		t.Fatal("contract step accepted a plain stored hash")
	}
	if !validToken("operator-token", Integration{TokenHash: sum}) {
		t.Fatal("a static integration's configured hash was refused")
	}
	other := credhash.For(credhash.FamilyOutbound).Store(AIAccessTokenColumn, sum[:])
	if validToken("operator-token", Integration{StoredTokenHash: other}) {
		t.Fatal("an AI access token digest was valid as an integration token")
	}
}
