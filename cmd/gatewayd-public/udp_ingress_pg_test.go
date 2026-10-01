package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

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
	pool.Close()
	failedStop, err := startUDPIngress(ctx, log, store, nil)
	if err == nil || failedStop != nil {
		t.Fatalf("closed database startup stop present=%v err=%v", failedStop != nil, err)
	}
}
