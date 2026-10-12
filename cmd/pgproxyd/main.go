// Command pgproxyd is the PostgreSQL proxy of native managed databases
// (ADR-1027), the only public listener on a database node. It
// authenticates clients by SCRAM before waking anything, wakes databases
// through schedd and reaches them through vmmd. See pkg/pgproxy.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/dbcreds"
	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/pgproxy"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	configPath := flag.String("config", "/etc/faas/pgproxyd.toml", "path to pgproxyd.toml")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *configPath, log); err != nil {
		log.Error("pgproxyd: exiting", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string, log *slog.Logger) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertPath, cfg.TLSKeyPath)
	if err != nil {
		return fmt.Errorf("pgproxyd: server certificate: %w", err)
	}
	decoy, err := readSecret(cfg.DecoySecretPath, 32)
	if err != nil {
		return err
	}
	dbURL, err := readSecret(cfg.DBURLPath, 1)
	if err != nil {
		return err
	}
	pool, err := db.OpenReadOnlyWithAppName(ctx, string(dbURL), "faas-pgproxyd")
	if err != nil {
		return fmt.Errorf("pgproxyd: database: %w", err)
	}
	defer pool.Close()
	verifiers, err := dbcreds.NewVerifierStore(pool, cfg.VerifierCacheTTL)
	if err != nil {
		return err
	}
	scheddTLS, err := wire.LoadClientTLSConfigWithPrefix("schedd_client", cfg.ScheddClientCertPath, cfg.ScheddClientKeyPath, cfg.ScheddClientCAPath)
	if err != nil {
		return err
	}
	scheddConn, err := overlay.Dial(ctx, overlay.New(cfg.ScheddTarget), scheddTLS)
	if err != nil {
		return fmt.Errorf("pgproxyd: dial schedd: %w", err)
	}
	defer func() { _ = scheddConn.Close() }()
	vmmdTLS, err := wire.LoadClientTLSConfigWithPrefix("vmmd_client", cfg.VmmdClientCertPath, cfg.VmmdClientKeyPath, cfg.VmmdClientCAPath)
	if err != nil {
		return err
	}
	dialer := pgproxy.NewVmmdDialer(vmmdTLS)
	defer func() { _ = dialer.Close() }()
	proxy, err := pgproxy.New(pgproxy.Config{
		TLS: &tls.Config{Certificates: []tls.Certificate{cert}}, HostSuffix: cfg.HostSuffix, DecoySecret: decoy,
	}, verifiers, pgproxy.NewScheddWaker(scheddpb.NewScheddClient(scheddConn)), dialer, log)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("pgproxyd: listen %s: %w", cfg.ListenAddr, err)
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	log.Info("pgproxyd: listening", "addr", ln.Addr().String(), "host_suffix", cfg.HostSuffix)
	return proxy.Serve(ctx, ln)
}
