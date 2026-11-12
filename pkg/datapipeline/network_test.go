package datapipeline

import (
	"context"
	"net/netip"
	"net/url"
	"slices"
	"strings"
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

func TestNativeSubscriberUsesAllQualifiedAddressesWithOriginalTLSHost(t *testing.T) {
	u, err := url.Parse("postgres://replicator:p%40ss@db.example:5544/orders?sslmode=verify-full&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	addresses := []netip.Addr{
		netip.MustParseAddr("2001:db8::2"), netip.MustParseAddr("192.0.2.4"),
		netip.MustParseAddr("::ffff:192.0.2.4"), netip.MustParseAddr("203.0.113.9"),
		netip.MustParseAddr("::"), netip.MustParseAddr("ff02::1"), netip.MustParseAddr("fe80::1"), {},
	}
	qualified := qualifiedSourceAddresses(addresses, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("::/0")})
	if !slices.Equal(qualified, []string{"192.0.2.4", "2001:db8::2"}) {
		t.Fatal("address qualification or deduplication changed", qualified)
	}
	pinned, err := nativeSourceConninfo(u, qualified)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := url.Parse(pinned)
	if err != nil {
		t.Fatal(err)
	}
	q := actual.Query()
	password, _ := actual.User.Password()
	if q.Get("hostaddr") != strings.Join(qualified, ",") || q.Get("host") != "db.example,db.example" || actual.Host != u.Host || actual.Path != u.Path || password != "p@ss" || q.Get("sslmode") != "verify-full" || q.Get("sslrootcert") != "system" || q.Get("connect_timeout") != "1" {
		t.Fatal("fallback lost address, identity or TLS settings")
	}
	if u.Query().Has("hostaddr") {
		t.Fatal("building native connection changed the original source URL")
	}
	if _, err = nativeSourceConninfo(u, nil); err == nil {
		t.Fatal("native connection escaped an empty qualified address set")
	}
	for _, timeout := range []string{"", "0", "120"} {
		q := u.Query()
		q.Set("connect_timeout", timeout)
		u.RawQuery = q.Encode()
		pinned, err := nativeSourceConninfo(u, qualified)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := url.Parse(pinned)
		if err != nil || actual.Query().Get("connect_timeout") != "5" {
			t.Fatal("native fallback lost its connection bound", err)
		}
	}
}
