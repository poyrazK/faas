// Command s3-gatewayd serves the branded Gregale Object Storage endpoint.
// Caddy terminates TLS for s3.gregale.dev and forwards path-style S3 traffic
// to this loopback listener. The daemon validates Gregale-issued SigV4
// credentials, then routes each logical bucket through its immutable provider
// placement; upstream endpoints and credentials stay private.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	defaultListenAddr  = "127.0.0.1:8084"
	defaultControlAddr = "127.0.0.1:9096"
)

func main() {
	wire.Daemon("s3-gatewayd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	if err := role.Require("s3-gatewayd", role.FromConfig("", "FAAS_S3_GATEWAY_ROLE"), role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}
	registry, err := objectstorage.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("s3-gatewayd: load object storage: %w", err)
	}
	if registry == nil {
		return errors.New("s3-gatewayd: FAAS_OBJECT_STORAGE_CONFIG is required")
	}
	identities, err := loadIdentities(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, "", "s3-gatewayd")
	if err != nil {
		return fmt.Errorf("s3-gatewayd: open db: %w", err)
	}
	defer pool.Close()
	store := state.NewPgStore(pool)
	if count, err := s3gateway.RekeyCredentials(ctx, store, identities); err != nil {
		return fmt.Errorf("s3-gatewayd: rekey credentials: %w", err)
	} else if count > 0 {
		log.Info("s3-gatewayd: credentials re-sealed under current host identity", "count", count)
	}

	enabled := runtimeconfig.NewBoolFlag(false)
	watcher := runtimeconfig.New(store, pool, []string{runtimeconfig.KeyS3}, func(_ context.Context, key string, value json.RawMessage, _ int64) error {
		if key != runtimeconfig.KeyS3 {
			return nil
		}
		flag, err := runtimeconfig.Bool(value)
		if err != nil {
			return err
		}
		enabled.Store(flag)
		return nil
	}, log)
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("s3-gatewayd: initial runtime config reconcile failed", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("s3-gatewayd: runtime config watcher exited", "err", err)
		}
	}()

	handler, err := s3gateway.New(s3gateway.Config{
		Registry: registry, Store: store, Enabled: enabled.Load, SpoolDir: os.Getenv("FAAS_S3_GATEWAY_SPOOL_DIR"), Log: log,
		OpenSecret: func(sealed []byte) (string, error) {
			namespace, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
			if err != nil || namespace != s3gateway.CredentialSecretNamespace || len(plaintext) != 40 {
				return "", errors.New("s3-gatewayd: credential secret could not be opened")
			}
			return string(plaintext), nil
		},
	})
	if err != nil {
		return err
	}

	listenAddr := envOr("FAAS_S3_GATEWAY_LISTEN_ADDR", defaultListenAddr)
	controlAddr := envOr("FAAS_S3_GATEWAY_CONTROL_ADDR", defaultControlAddr)
	if !s3gateway.IsLoopbackAddress(listenAddr) || !s3gateway.IsLoopbackAddress(controlAddr) {
		return errors.New("s3-gatewayd: data and control listeners must be loopback addresses behind the TLS edge")
	}
	dataListener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("s3-gatewayd: listen data: %w", err)
	}
	defer dataListener.Close()
	controlListener, err := net.Listen("tcp", controlAddr)
	if err != nil {
		return fmt.Errorf("s3-gatewayd: listen control: %w", err)
	}
	defer controlListener.Close()

	dataServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	controlMux := http.NewServeMux()
	controlMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	controlMux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		probeCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(probeCtx); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	controlServer := &http.Server{Handler: controlMux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}

	log.Info("s3-gatewayd: listening", "data_addr", dataListener.Addr().String(), "control_addr", controlListener.Addr().String(), "endpoint", registry.PublicEndpoint, "region", registry.PublicRegion)
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- dataServer.Serve(dataListener) }()
	go func() { errorsCh <- controlServer.Serve(controlListener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		dataErr := dataServer.Shutdown(shutdownCtx)
		controlErr := controlServer.Shutdown(shutdownCtx)
		return errors.Join(dataErr, controlErr)
	case serveErr := <-errorsCh:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return serveErr
	}
}

func loadIdentities(getenv func(string) string) ([]*age.X25519Identity, error) {
	currentPath := getenv("FAAS_HOST_AGE_IDENTITY_PATH")
	if currentPath == "" {
		return nil, errors.New("s3-gatewayd: FAAS_HOST_AGE_IDENTITY_PATH is required")
	}
	current, err := secretbox.LoadHostKey(currentPath)
	if err != nil {
		return nil, fmt.Errorf("s3-gatewayd: load host age identity: %w", err)
	}
	identities := []*age.X25519Identity{current}
	if previousPath := getenv("FAAS_HOST_AGE_PREVIOUS_IDENTITY_PATH"); previousPath != "" {
		previous, err := secretbox.LoadHostKey(previousPath)
		if err != nil {
			return nil, fmt.Errorf("s3-gatewayd: load previous host age identity: %w", err)
		}
		identities = append(identities, previous)
	}
	return identities, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
