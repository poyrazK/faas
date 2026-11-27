package main_test

import (
	"net/http"
	"strings"
	"testing"
)

// Prod hunt r64: `key create ""` minted a key that `key list` shows as "-",
// and `domain add https://shop.example.com` answered `invalid character in
// DNS label "https://shop"`. Both are refused before any request.
func TestBlankKeyLabelAndURLDomainRefusedLocally(t *testing.T) {
	requests := 0
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusTeapot)
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"key", "create", ""}, "the key label cannot be blank"},
		{[]string{"key", "create", "   ", "--scopes", "apps:read"}, "the key label cannot be blank"},
		{[]string{"domain", "add", "https://shop.example.com/", "--app", "web"}, "pass only its host name: shop.example.com"},
		{[]string{"domain", "add", "shop.example.com/path", "--app", "web"}, "pass only its host name: shop.example.com"},
	} {
		code, out := r63Run(t, tc.args...)
		if code == 0 || !strings.Contains(out, tc.want) {
			t.Errorf("%q = %d, want %q:\n%s", tc.args, code, tc.want, out)
		}
	}
	if requests != 0 {
		t.Fatalf("refused input sent %d requests", requests)
	}
}
