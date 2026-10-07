package main

// adr: 613

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicGuardFactProbe struct {
	called   chan state.RuntimeUpgradePublicEdgeMember
	finished chan struct{}
	bounded  bool
}

func (p *publicGuardFactProbe) RecordRuntimeUpgradePublicEdgeGuard(ctx context.Context, m state.RuntimeUpgradePublicEdgeMember) error {
	deadline, ok := ctx.Deadline()
	p.bounded = ok && time.Until(deadline) <= api.RuntimeUpgradeGatewayRepairTimeout
	p.called <- m
	<-ctx.Done()
	close(p.finished)
	return ctx.Err()
}

func TestPublicEdgeConfirmationRequiresInstalledGuardAndFreshIdentity(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	plain := gateway.NewInternalReverseProxy(gateway.NewTCPDialer("127.0.0.1:1"), nil, log, true)
	if o, err := prepareRuntimePublicEdgeObserver(t.Context(), plain, nil, runtimePublicEdgeConfig{}, func(string) string { return "" }, log); err != nil || o != nil {
		t.Fatal("default off changed behavior", o, err)
	}
	slot := uuid.NewString()
	getenv := func(key string) string {
		switch key {
		case "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_CONFIRMATION":
			return "1"
		case "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_SLOT_ID":
			return slot
		}
		return ""
	}
	probe := &publicGuardFactProbe{}
	if _, err := prepareRuntimePublicEdgeObserver(t.Context(), plain, probe, runtimePublicEdgeConfig{}, getenv, log); err == nil {
		t.Fatal("unguarded process claimed an observation")
	}
	store := &ingressBindingFixture{slot: uuid.NewString(), session: uuid.NewString()}
	proxy := ingressTestProxy(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), store)
	config := publicEdgeConfig(internalUpstreamUnix, true, defaultListenAddr, nil, func(string) string { return "" })
	first, err := prepareRuntimePublicEdgeObserver(t.Context(), proxy, probe, config, getenv, log)
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepareRuntimePublicEdgeObserver(t.Context(), proxy, probe, config, getenv, log)
	if err != nil {
		t.Fatal(err)
	}
	if first.member.SlotID != slot || first.member.SessionID == second.member.SessionID || first.member.ConfigSHA256 != second.member.ConfigSHA256 || len(first.member.ConfigSHA256) != sha256.Size*2 {
		t.Fatal(first.member, second.member)
	}
	if _, err := prepareRuntimePublicEdgeObserver(t.Context(), proxy, nil, config, getenv, log); err == nil {
		t.Fatal("missing fact store accepted")
	}
	if _, err := prepareRuntimePublicEdgeObserver(t.Context(), proxy, probe, config, func(key string) string {
		if key == "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_SLOT_ID" {
			return "bad"
		}
		return getenv(key)
	}, log); err == nil {
		t.Fatal("bad slot accepted")
	}
	proxy.Dialer = plain.Dialer
	if _, err := prepareRuntimePublicEdgeObserver(t.Context(), proxy, probe, config, getenv, log); err == nil {
		t.Fatal("unguarded upgrade path accepted")
	}
}

func TestPublicEdgeConfigFingerprintTracksSelectedPathAndTrustPolicy(t *testing.T) {
	values := map[string]string{"FAAS_NODE_NAME": "edge-one", "FAAS_INTERNAL_TARGET": "tcp://stale:8080", "FAAS_INTERNAL_SOCKET": "/unused.sock", "FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN": "secret"}
	getenv := func(k string) string { return values[k] }
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("10.0.0.0/8")}
	base := publicEdgeConfig(internalUpstreamDatabase, false, "127.0.0.1:8080", prefixes, getenv)
	values["FAAS_INTERNAL_TARGET"], values["FAAS_INTERNAL_SOCKET"], values["FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN"] = "tcp://other:8080", "/other-unused.sock", "another secret"
	if other := publicEdgeConfig(internalUpstreamDatabase, false, "127.0.0.1:8080", []netip.Prefix{prefixes[1], prefixes[0]}, getenv); !reflect.DeepEqual(other, base) {
		t.Fatal("unused fallback or CIDR order changed selected config", base, other)
	}
	values["FAAS_INTERNAL_TARGET"] = " tcp://user:credential@actual:8080?unused=1 "
	if config := publicEdgeConfig(internalUpstreamStatic, false, base.ListenAddress, nil, getenv); config.UpstreamTarget != "actual:8080" {
		t.Fatal("static config did not use the actual dial target", config)
	}
	hash := func(c runtimePublicEdgeConfig) [sha256.Size]byte {
		body, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(body)
	}
	for _, changed := range []runtimePublicEdgeConfig{publicEdgeConfig(internalUpstreamUnix, false, base.ListenAddress, prefixes, getenv), publicEdgeConfig(internalUpstreamStatic, false, base.ListenAddress, prefixes, getenv), publicEdgeConfig(internalUpstreamDatabase, true, base.ListenAddress, prefixes, getenv), publicEdgeConfig(internalUpstreamDatabase, false, "127.0.0.1:9090", prefixes, getenv), publicEdgeConfig(internalUpstreamDatabase, false, base.ListenAddress, nil, getenv)} {
		if hash(changed) == hash(base) {
			t.Fatal("selected config change retained digest", changed)
		}
	}
}

func TestPublicEdgeGuardFactsStartAtListenerAndStopOnShutdown(t *testing.T) {
	probe := &publicGuardFactProbe{called: make(chan state.RuntimeUpgradePublicEdgeMember, 1), finished: make(chan struct{})}
	member := state.RuntimeUpgradePublicEdgeMember{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: "fixture"}
	o := &runtimePublicEdgeObserver{member: member, store: probe, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer server.Close()
	stop := o.attach(t.Context(), server.Config)
	defer stop()
	select {
	case <-probe.called:
		t.Fatal("fact published before listening")
	default:
	}
	server.Start()
	select {
	case got := <-probe.called:
		if got != member || !probe.bounded {
			t.Fatal(got, probe.bounded)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not start bounded publication")
	}
	if err := server.Config.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-probe.finished:
	case <-time.After(time.Second):
		t.Fatal("shutdown retained in-flight fact write")
	}
}
