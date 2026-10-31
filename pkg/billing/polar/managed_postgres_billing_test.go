package polar

import (
	"strings"
	"testing"
	"time"
)

// adr: 20261031165104 — managed PostgreSQL overage billing defaults off, loads
// in shadow from a UTC-month boundary, and refuses live until its publish
// exists.
func TestManagedPostgresBillingPolicy(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := p.ManagedPostgresBillingPolicy(); ok {
		t.Fatal("managed PostgreSQL billing is on by default")
	}
	cfg := testConfig("http://example.test")
	cfg.ManagedPostgresBillingMode, cfg.ManagedPostgresBillingFrom = ManagedPostgresBillingShadow, "2026-11-01T00:00:00Z"
	p, err = NewProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider, mode, from, ok := p.ManagedPostgresBillingPolicy()
	if !ok || provider != "polar" || mode != "shadow" || !from.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("policy = %s %s %v %v", provider, mode, from, ok)
	}
	for _, tc := range []struct{ mode, from, want string }{
		{"live", "2026-11-01T00:00:00Z", "not available yet"},
		{"sometimes", "2026-11-01T00:00:00Z", "invalid managed PostgreSQL billing mode"},
		{"shadow", "2026-11-02T00:00:00Z", "UTC-month boundary"},
		{"shadow", "", "UTC-month boundary"},
	} {
		cfg := testConfig("http://example.test")
		cfg.ManagedPostgresBillingMode, cfg.ManagedPostgresBillingFrom = tc.mode, tc.from
		if _, err := NewProvider(cfg, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("mode %q from %q = %v, want %q", tc.mode, tc.from, err, tc.want)
		}
	}
}
