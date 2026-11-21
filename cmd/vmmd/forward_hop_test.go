package main

// adr: 20261121202814 — with the switch off, or without the TCP mTLS
// listener, vmmd's gRPC listener is not touched.

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestWrapForwardHopLeavesListenerUnlessOn(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := prometheus.NewRegistry()
	env := func(v string) func(string) string { return func(string) string { return v } }
	tlsCfg := &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}

	if got := wrapForwardHop(context.Background(), lis, tlsCfg, nil, env(""), reg, log); got != lis {
		t.Fatal("switch off wrapped the listener")
	}
	if got := wrapForwardHop(context.Background(), lis, tlsCfg, nil, env("0"), reg, log); got != lis {
		t.Fatal("switch 0 wrapped the listener")
	}
	if got := wrapForwardHop(context.Background(), lis, nil, nil, env("1"), reg, log); got != lis {
		t.Fatal("a listener without TLS was wrapped")
	}
	// No service: the hop refuses to start and gRPC stays alone.
	if got := wrapForwardHop(context.Background(), lis, tlsCfg, nil, env("1"), reg, log); got != lis {
		t.Fatal("a hop without a service was started")
	}
}
