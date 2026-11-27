package main

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

func TestParseInternalEgressCIDRs(t *testing.T) {
	got, err := parseInternalEgressCIDRs([]string{"51.222.39.227", "3.23.186.0/24", "10.1.2.3/8"})
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("51.222.39.227/32"),
		netip.MustParsePrefix("3.23.186.0/24"),
		netip.MustParsePrefix("10.0.0.0/8"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"2001:db8::/32", "::1", "neon.tech", ""} {
		if _, err := parseInternalEgressCIDRs([]string{bad}); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestInternalEgressResolverKeepsAddressesForTTL(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	answers := map[string][]netip.Addr{
		"db.example": {netip.MustParseAddr("3.143.47.40"), netip.MustParseAddr("2001:db8::1")},
	}
	var lookupErr error
	var applied []netip.Prefix
	r := &internalEgressResolver{
		static: []netip.Prefix{netip.MustParsePrefix("51.222.39.227/32")},
		hosts:  []string{"db.example"},
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			if lookupErr != nil {
				return nil, lookupErr
			}
			return answers["db.example"], nil
		},
		now:      func() time.Time { return now },
		apply:    func(p []netip.Prefix) { applied = p },
		lastSeen: make(map[netip.Addr]time.Time),
	}
	ctx := context.Background()
	prefixes := func(s ...string) []netip.Prefix {
		out := make([]netip.Prefix, len(s))
		for i, v := range s {
			out[i] = netip.MustParsePrefix(v)
		}
		return out
	}

	r.refresh(ctx)
	if want := prefixes("3.143.47.40/32", "51.222.39.227/32"); !reflect.DeepEqual(applied, want) {
		t.Fatalf("first refresh = %v, want %v (IPv6 dropped)", applied, want)
	}

	// The endpoint rotates: the old address stays internal within the TTL.
	now = now.Add(time.Hour)
	answers["db.example"] = []netip.Addr{netip.MustParseAddr("3.23.186.13")}
	r.refresh(ctx)
	if want := prefixes("3.143.47.40/32", "3.23.186.13/32", "51.222.39.227/32"); !reflect.DeepEqual(applied, want) {
		t.Fatalf("after rotation = %v, want %v", applied, want)
	}

	// A DNS outage never shrinks the set.
	lookupErr = errors.New("servfail")
	now = now.Add(time.Hour)
	r.refresh(ctx)
	if len(applied) != 3 {
		t.Fatalf("after failed lookup = %v, want 3 prefixes", applied)
	}

	// Past the TTL only the address DNS still returns is kept.
	lookupErr = nil
	now = now.Add(internalEgressAddressTTL)
	r.refresh(ctx)
	if want := prefixes("3.23.186.13/32", "51.222.39.227/32"); !reflect.DeepEqual(applied, want) {
		t.Fatalf("after TTL = %v, want %v", applied, want)
	}
}
