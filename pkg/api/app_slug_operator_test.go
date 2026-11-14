package api

import "testing"

// ADR-20261107180000 (R8-11): the labels of edgednsd's managed host names
// are reserved for allocation; the fixed platform list is unchanged.
func TestOperatorReservedAppSlugs(t *testing.T) {
	t.Cleanup(func() { SetOperatorReservedAppSlugs(nil) })
	if IsReservedAppSlug("direct-pilot") {
		t.Fatal("reserved before the cell named it")
	}
	SetOperatorReservedAppSlugs([]string{" Direct-Pilot ", "", "edge-canary"})
	for _, slug := range []string{"direct-pilot", "edge-canary"} {
		if !IsReservedAppSlug(slug) {
			t.Errorf("%s is not reserved", slug)
		}
		if IsPlatformReservedSlug(slug) {
			t.Errorf("%s became a platform host label: an existing app with it would stop routing", slug)
		}
	}
	if IsReservedAppSlug("shop") {
		t.Fatal("an ordinary name reserved")
	}
}
