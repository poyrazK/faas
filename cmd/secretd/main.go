// Command secretd is the control plane's opener of customer runtime secrets
// (ADR-20261110210000 §3).
//
// It holds the runtime seal domain's identity, which compute nodes stop
// holding once every vmmd asks secretd instead. A caller's mutual-TLS client
// leaf names its compute node; the handshake admits only registered nodes,
// and pkg/secretd opens an app's entries only for its owner node or a peer
// the owner granted, auditing each open before any plaintext leaves.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"filippo.io/age"
	"google.golang.org/grpc"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/secretd/secretpg"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("secretd", run)
}

// grantSweepInterval is how often expired grants are deleted. An expired
// grant already opens nothing; the sweep only keeps the table small.
const grantSweepInterval = 5 * time.Minute

type config struct {
	listen, certPath, keyPath, caPath, identityPath string
	appsPerNodeHour                                 int
}

func configFromEnv(getenv func(string) string) (config, error) {
	cfg := config{
		listen:       getenv("FAAS_SECRETD_LISTEN"),
		certPath:     getenv("FAAS_SECRETD_TLS_CERT_PATH"),
		keyPath:      getenv("FAAS_SECRETD_TLS_KEY_PATH"),
		caPath:       getenv("FAAS_SECRETD_TLS_CA_PATH"),
		identityPath: getenv("FAAS_SECRETD_RUNTIME_IDENTITY"),
	}
	target, err := wire.ParseTarget(cfg.listen)
	if err != nil || target.Scheme != wire.SchemeTCP {
		return config{}, fmt.Errorf("secretd: FAAS_SECRETD_LISTEN=%q: want tcp://<private address>:<port>", cfg.listen)
	}
	if cfg.certPath == "" || cfg.keyPath == "" || cfg.caPath == "" {
		return config{}, errors.New("secretd: FAAS_SECRETD_TLS_{CERT,KEY,CA}_PATH are required: secretd serves mutual TLS only")
	}
	if cfg.identityPath == "" {
		return config{}, errors.New("secretd: FAAS_SECRETD_RUNTIME_IDENTITY is required: the runtime seal domain's identity credential")
	}
	if raw := getenv("FAAS_SECRETD_APPS_PER_NODE_HOUR"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return config{}, fmt.Errorf("secretd: FAAS_SECRETD_APPS_PER_NODE_HOUR=%q: want a positive integer", raw)
		}
		cfg.appsPerNodeHour = n
	}
	return cfg, nil
}

// loadIdentities loads the runtime domain identity and, during a rotation,
// its previous twin (the credential named <name>_previous beside it).
func loadIdentities(path string) ([]*age.X25519Identity, error) {
	ids, err := sealdomain.LoadReaderIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("secretd: runtime identity: %w", err)
	}
	return ids, nil
}

func run(ctx context.Context, log *slog.Logger) error {
	if err := role.Require("secretd", role.FromConfig("", "FAAS_SECRETD_ROLE"),
		role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}
	cfg, err := configFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	identities, err := loadIdentities(cfg.identityPath)
	if err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, "", "faas-secretd")
	if err != nil {
		return fmt.Errorf("secretd: open database: %w", err)
	}
	defer pool.Close()
	store := secretpg.New(pool, state.NewPgStore(pool))

	// The handshake admits only registered compute nodes' leaves; the
	// handler maps the leaf's CN to the node and checks ownership or grant.
	nodes := wire.NewPGNodeVerifier(wire.NewPGNodeLoader(pool), log)
	if _, err := nodes.Refresh(ctx); err != nil {
		return fmt.Errorf("secretd: node registry: %w", err)
	}
	go func() {
		ch, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyComputeNodeChanged}, log)
		if err != nil {
			log.Error("secretd: node registry LISTEN failed", "err", err)
			return
		}
		if err := nodes.Run(ctx, ch); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("secretd: node registry watch exited", "err", err)
		}
	}()
	go sweepGrants(ctx, store, log)

	tlsCfg, err := wire.LoadServerTLSConfigWithPrefixAndVerifier("secretd_", cfg.certPath, cfg.keyPath, cfg.caPath, nodes)
	if err != nil {
		return fmt.Errorf("secretd: server TLS: %w", err)
	}
	lis, err := wire.Listen(ctx, cfg.listen, tlsCfg)
	if err != nil {
		return fmt.Errorf("secretd: listen %s: %w", cfg.listen, err)
	}
	srv := grpc.NewServer(wire.ServerCredsOrEmpty(tlsCfg)...)
	secretdpb.RegisterSecretdServer(srv, secretd.NewServer(store, identities, cfg.appsPerNodeHour, log))
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	log.Info("secretd: serving", "listen", cfg.listen, "identities", len(identities))
	if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("secretd: serve: %w", err)
	}
	return nil
}

type grantSweeper interface {
	DeleteExpiredGrants(ctx context.Context, before time.Time) (int64, error)
}

func sweepGrants(ctx context.Context, store grantSweeper, log *slog.Logger) {
	t := time.NewTicker(grantSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := store.DeleteExpiredGrants(ctx, time.Now()); err != nil {
				log.Warn("secretd: sweep expired grants", "err", err)
			} else if n > 0 {
				log.Info("secretd: swept expired grants", "deleted", n)
			}
		}
	}
}
