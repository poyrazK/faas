package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/onebox-faas/faas/pkg/wire"
)

// rotatingClientTLS loads one of pgproxyd's internal client leaves (the
// pgproxyd.faas identity it dials schedd and vmmd with) and swaps it on
// SIGHUP, which the daily internal-PKI renewal sends after installing a
// new one. Every later handshake presents the renewed leaf; established
// connections keep theirs, so no session is dropped.
func rotatingClientTLS(ctx context.Context, prefix, certPath, keyPath, caPath string, log *slog.Logger) (*tls.Config, error) {
	load := func() (*tls.Config, error) {
		return wire.LoadClientTLSConfigWithPrefix(prefix, certPath, keyPath, caPath)
	}
	boot, err := load()
	if err != nil || boot == nil {
		return boot, err
	}
	rotator := wire.NewTLSRotator(boot)
	cfg, err := wire.LoadClientTLSConfigWithReload(prefix, certPath, keyPath, caPath, rotator.Reload(boot))
	if err != nil {
		return nil, fmt.Errorf("pgproxyd: %s TLS: %w", prefix, err)
	}
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		<-ctx.Done()
		signal.Stop(hup)
	}()
	go wire.WatchTLSReload(ctx, log, hup, rotator, load)
	return cfg, nil
}
