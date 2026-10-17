// adr: 570
package main

import "testing"

func TestPublicTrafficSecurityStartupRefusesMissingStore(t *testing.T) {
	if _, err := newPublicTrafficRevocationRegistry(t.Context(), nil); err == nil {
		t.Fatal("missing production security store passed public startup verification")
	}
}
