// adr: 20261105102920
package environmentrollback

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEnvironmentRollbackScopedKeyCompatibility(t *testing.T) {
	prefix := "org " + strings.Repeat("a", 36) + " project " + strings.Repeat("b", 36) + " actor " + strings.Repeat("c", 36) + " credential " + strings.Repeat("d", 36) + "\n"
	for _, raw := range []string{"legacy-key", strings.Repeat("x", 78), strings.Repeat("🙂", 50)} {
		key := prefix + raw
		if utf8.RuneCountInString(key) > 255 {
			t.Fatal("invalid compatibility fixture")
		}
		if BoundedIdempotencyKey(key) != key {
			t.Fatal("previously storable key changed")
		}
	}
	key := prefix + "gmcp-env-rollback-" + strings.Repeat("e", 64)
	if utf8.RuneCountInString(key) <= 255 {
		t.Fatal("company MCP key does not exercise overflow")
	}
	bounded := BoundedIdempotencyKey(key)
	if bounded == key || len(bounded) > 255 || bounded != BoundedIdempotencyKey(key) || bounded == BoundedIdempotencyKey(strings.Replace(key, "credential d", "credential f", 1)) {
		t.Fatal("scoped retry key lost bounds, stability or credential isolation")
	}
}
