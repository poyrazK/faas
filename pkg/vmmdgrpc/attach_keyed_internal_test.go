package vmmdgrpc

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
)

// adr: 20261029202403 amendment 1 — vmmd takes the attach digest schedd
// forwards as apid stored it, plain or keyed under the attach ring, and
// checks the presented token with that ring.
func TestAttachRegistryChecksKeyedDigests(t *testing.T) {
	line, err := credhash.NewKeyLine(3)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	credhash.Set(credhash.FamilyAttach, &credhash.Policy{Ring: ring, KeyedWrites: true})
	t.Cleanup(func() { credhash.Set(credhash.FamilyAttach, nil) })

	sum := sha256.Sum256([]byte("attach-token"))
	keyed := credhash.For(credhash.FamilyAttach).Store(api.AttachTokenCredentialTag, sum[:])
	r := &appTaskAttachRegistry{}
	if _, err := r.register("i-1", keyed); err != nil {
		t.Fatalf("register keyed digest: %v", err)
	}
	if err := r.claim("i-1", "wrong-token", nil); !errors.Is(err, errAppTaskAttachTokenDenied) {
		t.Fatalf("wrong token = %v", err)
	}
	if err := r.claim("i-1", "attach-token", nil); err != nil {
		t.Fatalf("keyed digest refused its token: %v", err)
	}
	if _, err := r.register("i-2", sum[:]); err != nil {
		t.Fatalf("register plain digest: %v", err)
	}
	if err := r.claim("i-2", "attach-token", nil); !errors.Is(err, errAppTaskAttachTokenDenied) {
		t.Fatalf("contract step accepted a plain digest: %v", err)
	}
	for _, bad := range [][]byte{nil, []byte("short"), append(keyed, 0)} {
		if _, err := r.register("i-bad", bad); !errors.Is(err, errAppTaskAttachTokenDenied) {
			t.Fatalf("register %x = %v", bad, err)
		}
	}
}
