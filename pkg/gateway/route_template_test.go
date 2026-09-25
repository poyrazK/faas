// adr: 242
package gateway

import (
	"strings"
	"testing"
)

func TestInferredObservedPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/profile/238", "/profile/{id}"},
		{"/profile/392", "/profile/{id}"},
		{"/orders/550e8400-e29b-41d4-a716-446655440000", "/orders/{id}"},
		{"/users/me", "/users/me"},
		{"/v1/products", "/v1/products"},
		{"/2024", "/2024"},
		{"/objects/0123456789abcdef01234567", "/objects/{id}"},
		{"/" + strings.Repeat("x", 249), otherRouteLabel},
	} {
		if got := inferredObservedPath(tc.in); got != tc.want {
			t.Errorf("inferredObservedPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
