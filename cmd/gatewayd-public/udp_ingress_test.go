package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestUDPSourcePrefixes(t *testing.T) {
	for _, raw := range []string{"", " ", "127.0.0.1", "invalid", "::/0", "127.0.0.0/8,", "127.0.0.0/8,bad"} {
		if _, err := udpSourcePrefixes(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	prefixes, err := udpSourcePrefixes(" 127.1.2.3/8, 192.0.2.0/24 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 2 || prefixes[0].String() != "127.0.0.0/8" || prefixes[1].String() != "192.0.2.0/24" {
		t.Fatalf("prefixes=%v", prefixes)
	}
}
func TestUDPIngressOptIn(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("FAAS_UDPD_ENABLED", "0")
	t.Setenv("FAAS_UDPD_ALLOWED_SOURCE_CIDRS", "invalid")
	stop, err := startUDPIngress(context.Background(), log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	stop()
	t.Setenv("FAAS_UDPD_ENABLED", "1")
	if _, err := startUDPIngress(context.Background(), log, nil, nil); err == nil {
		t.Fatal("enabled ingress accepted missing store")
	}
}

func TestUDPBindHost(t *testing.T) {
	for _, raw := range []string{"", "localhost", "example.com", "::", "::ffff:127.0.0.1", "127.0.0.1:40100", "127.0.0.1/8", "999.0.0.1"} {
		if _, err := udpBindHost(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"0.0.0.0", "127.0.0.1", " 192.0.2.10 "} {
		if _, err := udpBindHost(raw); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
}
