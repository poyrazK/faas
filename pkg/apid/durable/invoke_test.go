// adr: 712
package durable

import (
	"testing"

	"github.com/google/uuid"
)

func TestDurableEntityAppAllowlistRequiresExplicitAppIDs(t *testing.T) {
	for _, raw := range []string{"", "*", "not-an-id", uuid.Nil.String()} {
		if _, err := durableEntityAppAllowlist(raw); err == nil {
			t.Fatalf("accepted preview allowlist %q", raw)
		}
	}
	id := uuid.NewString()
	apps, err := durableEntityAppAllowlist(" " + id + " ")
	if err != nil || !apps[id] || len(apps) != 1 {
		t.Fatal(apps, err)
	}
}
