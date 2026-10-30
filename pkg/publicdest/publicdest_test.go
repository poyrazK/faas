package publicdest

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"testing"
)

func TestOnlyGloballyReachableAddressesPass(t *testing.T) {
	for addr, want := range map[string]bool{
		"1.1.1.1":         true,
		"2606:4700::1111": true,
		"127.0.0.1":       false,
		"10.0.0.2":        false,
		"169.254.169.254": false,
		"100.64.0.1":      false,
		"192.0.2.10":      false,
		"::1":             false,
		"fd00::1":         false,
		"64:ff9b::a00:1":  false,
	} {
		if got := isPublicDestination(netip.MustParseAddr(addr)); got != want {
			t.Errorf("isPublicDestination(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestValidateOriginRefusesUnsafeShapes(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user@example.com", "https://example.com?x=1", "https://127.0.0.1", "https://example.com:99999"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateOrigin(context.Background(), u); !errors.Is(err, ErrUnsafe) {
			t.Errorf("ValidateOrigin(%s) = %v, want ErrUnsafe", raw, err)
		}
	}
}
