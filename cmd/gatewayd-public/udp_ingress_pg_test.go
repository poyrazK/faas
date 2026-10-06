package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestUDPIngressPostgresStartupAndShutdown(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv("FAAS_UDPD_ENABLED", "1")
	t.Setenv("FAAS_UDPD_BIND_HOST", "127.0.0.1")
	t.Setenv("FAAS_UDPD_ALLOWED_SOURCE_CIDRS", "127.0.0.0/8")
	t.Setenv("FAAS_UDPD_SCHEDD_TARGET", "unix://"+t.TempDir()+"/schedd.sock")
	for _, name := range []string{"FAAS_UDPD_VMMD_TLS_CERT_PATH", "FAAS_UDPD_VMMD_TLS_KEY_PATH", "FAAS_UDPD_VMMD_TLS_CA_PATH", "FAAS_UDPD_SCHEDD_TLS_CERT_PATH", "FAAS_UDPD_SCHEDD_TLS_KEY_PATH", "FAAS_UDPD_SCHEDD_TLS_CA_PATH"} {
		t.Setenv(name, "")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := state.NewPgStore(pool)
	stop, err := startUDPIngress(ctx, log, store, nil)
	if err != nil || stop == nil {
		t.Fatalf("startup stop present=%v err=%v", stop != nil, err)
	}
	stop()
	stop()

	account, err := store.CreateAccount(ctx, "udp-startup@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "udp-startup", Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	var address *net.UDPAddr
	for port := api.UDPListenerPublicPortMin; port <= api.UDPListenerPublicPortMax; port++ {
		candidate := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
		probe, bindErr := net.ListenUDP("udp4", candidate)
		if bindErr == nil {
			address = candidate
			_ = probe.Close()
			break
		}
	}
	if address == nil {
		t.Fatal("no free public UDP port")
	}
	if _, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 5353, PublicPort: address.Port, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	stop, err = startUDPIngress(ctx, log, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if probe, err := net.ListenUDP("udp4", address); err == nil {
		_ = probe.Close()
		t.Fatal("ready ingress did not own enabled socket")
	}
	stop()
	probe, err := net.ListenUDP("udp4", address)
	if err != nil {
		t.Fatalf("shutdown retained socket: %v", err)
	}
	_ = probe.Close()
	pool.Close()
	failedStop, err := startUDPIngress(ctx, log, store, nil)
	if err == nil || failedStop != nil {
		t.Fatalf("closed database startup stop present=%v err=%v", failedStop != nil, err)
	}
}
