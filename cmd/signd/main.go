// Command signd is the control-plane artifact signer (ADR-20261109170000).
//
// It holds the fleet signing key and serves SignArtifact to compute nodes'
// imaged over mutual TLS. A caller's client leaf names its node; the
// handshake admits only registered compute nodes, and pkg/signd signs a
// layer only for the node building that deployment, once per storage key.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"google.golang.org/grpc"

	signdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/signd/v1"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/signd/signpg"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("signd", run)
}

type config struct {
	listen, certPath, keyPath, caPath, signKeyPath string
}

func configFromEnv(getenv func(string) string) (config, error) {
	cfg := config{
		listen:      getenv("FAAS_SIGND_LISTEN"),
		certPath:    getenv("FAAS_SIGND_TLS_CERT_PATH"),
		keyPath:     getenv("FAAS_SIGND_TLS_KEY_PATH"),
		caPath:      getenv("FAAS_SIGND_TLS_CA_PATH"),
		signKeyPath: getenv("FAAS_SIGN_KEY"),
	}
	if cfg.signKeyPath == "" {
		cfg.signKeyPath = cosign.DefaultSignKeyPath
	}
	target, err := wire.ParseTarget(cfg.listen)
	if err != nil || target.Scheme != wire.SchemeTCP {
		return config{}, fmt.Errorf("signd: FAAS_SIGND_LISTEN=%q: want tcp://<private address>:<port>", cfg.listen)
	}
	if cfg.certPath == "" || cfg.keyPath == "" || cfg.caPath == "" {
		return config{}, errors.New("signd: FAAS_SIGND_TLS_{CERT,KEY,CA}_PATH are required: signd serves mutual TLS only")
	}
	return cfg, nil
}

func run(ctx context.Context, log *slog.Logger) error {
	if err := role.Require("signd", role.FromConfig("", "FAAS_SIGND_ROLE"),
		role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}
	cfg, err := configFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	key, err := cosign.LoadPrivateKeyFile(cfg.signKeyPath)
	if err != nil {
		return fmt.Errorf("signd: sign key: %w", err)
	}
	objects, err := storage.BackendFromEnvContext(ctx)
	if err != nil {
		return fmt.Errorf("signd: %w", err)
	}
	pool, err := db.OpenWithAppName(ctx, "", "faas-signd")
	if err != nil {
		return fmt.Errorf("signd: open database: %w", err)
	}
	defer pool.Close()

	// The handshake admits only registered compute nodes' leaves; the
	// handler then matches the leaf's CN to the build's node.
	nodes := wire.NewPGNodeVerifier(wire.NewPGNodeLoader(pool), log)
	if _, err := nodes.Refresh(ctx); err != nil {
		return fmt.Errorf("signd: node registry: %w", err)
	}
	go func() {
		ch, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyComputeNodeChanged}, log)
		if err != nil {
			log.Error("signd: node registry LISTEN failed", "err", err)
			return
		}
		if err := nodes.Run(ctx, ch); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("signd: node registry watch exited", "err", err)
		}
	}()
	tlsCfg, err := wire.LoadServerTLSConfigWithPrefixAndVerifier("signd_", cfg.certPath, cfg.keyPath, cfg.caPath, nodes)
	if err != nil {
		return fmt.Errorf("signd: server TLS: %w", err)
	}
	lis, err := wire.Listen(ctx, cfg.listen, tlsCfg)
	if err != nil {
		return fmt.Errorf("signd: listen %s: %w", cfg.listen, err)
	}
	srv := grpc.NewServer(wire.ServerCredsOrEmpty(tlsCfg)...)
	signdpb.RegisterSigndServer(srv, signd.NewServer(signpg.New(pool), key, objects, log))
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	log.Info("signd: serving", "listen", cfg.listen)
	if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("signd: serve: %w", err)
	}
	return nil
}
