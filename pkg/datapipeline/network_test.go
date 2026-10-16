package datapipeline

import (
	"context"
	"net/netip"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/commit"
)

func TestNativeSubscriberPinsAddressAndVerifiedSystemTrust(t *testing.T) {
	e := Engine{Policy: commit.NetworkPolicy{Hosts: map[string]bool{"127.0.0.1": true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}}
	raw := "postgres://replicator:private@127.0.0.1/orders?sslmode=verify-full"
	pinned, err := e.sourceConninfo(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(pinned)
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "127.0.0.1" || u.Query().Get("hostaddr") != "127.0.0.1" || u.Query().Get("sslrootcert") != "system" || u.Query().Get("sslmode") != "verify-full" {
		t.Fatal("native subscriber lost its pinned address or TLS trust")
	}
	e.Policy.Prefixes = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	if _, err = e.sourceConninfo(context.Background(), raw); err == nil {
		t.Fatal("subscriber escaped the reviewed address policy")
	}
}
