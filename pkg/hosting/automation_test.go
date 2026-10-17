package hosting

import (
	"strings"
	"testing"
)

func TestHostingCIRunIdentityMustBeCanonical(t *testing.T) {
	// adr: 202610170925 — one textual identity maps to the same key in both stores.
	id := "a1111111-1111-4111-8111-111111111111"
	if !ValidCIRunID(id) {
		t.Fatal("canonical UUID rejected")
	}
	for _, invalid := range []string{strings.ToUpper(id), strings.ReplaceAll(id, "-", ""), "urn:uuid:" + id, "{" + id + "}", "bad"} {
		if ValidCIRunID(invalid) {
			t.Fatal("noncanonical run identity accepted", invalid)
		}
	}
}
