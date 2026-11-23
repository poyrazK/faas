package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Prod 2026-11-23: internal accounts' traffic drove the public API
// availability indicator; their apps leave every series the query reads.
func TestAvailabilityQueryExcluding(t *testing.T) {
	if got := apidserver.AvailabilityQueryExcluding(nil); got != apidserver.StatusAPIAvailabilityQuery {
		t.Fatalf("no exclusions changed the query:\n%s", got)
	}
	ids := []string{"16d12cbe-ce89-4a34-8d84-a1d5b9718851", "dc001401-baa6-4ae8-8817-8c45906a47a3"}
	got := apidserver.AvailabilityQueryExcluding(ids)
	want := `app!="-",app!~"16d12cbe-ce89-4a34-8d84-a1d5b9718851|dc001401-baa6-4ae8-8817-8c45906a47a3"`
	if n := strings.Count(got, want); n == 0 || n != strings.Count(apidserver.StatusAPIAvailabilityQuery, `app!="-"`) {
		t.Fatalf("every app matcher should exclude the apps (%d replaced):\n%s", n, got)
	}
	if !strings.Contains(got, "or vector(100)") {
		t.Fatalf("an all-excluded window must still read as available:\n%s", got)
	}
	// A regexp metacharacter is escaped for the regexp and its backslash
	// again for the PromQL string literal.
	if got := apidserver.AvailabilityQueryExcluding([]string{"a.b"}); !strings.Contains(got, `app!~"a\\.b"`) {
		t.Fatalf("metacharacter not escaped for PromQL:\n%s", got)
	}
}
