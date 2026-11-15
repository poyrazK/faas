package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

// secretdServerName is pki.SecretdServerName, the SAN on secretd's server
// leaf.
const secretdServerName = "secretd.faas"

// secretdLogDrainOpenTimeout bounds one header open; a drain worker that
// cannot start retries at the manager's next reconcile.
const secretdLogDrainOpenTimeout = 5 * time.Second

// logDrainHeaderOpener opens a drain's authentication header by reference.
type logDrainHeaderOpener func(ctx context.Context, appID, drainID string) (string, error)

// logDrainHeaderOpenerFromEnv picks who opens customer log drain headers
// (ADR-20261110210000 slice 3f). FAAS_GATEWAY_LOG_DRAIN_OPENER unset or
// "local" opens them with this node's edge identities. "secretd" asks the
// control plane's secretd at FAAS_GATEWAY_SECRETD_TARGET over mTLS with the
// node's secretd client leaf: secretd reads the sealed header from the drain
// row and opens it only for the app's owner node, which is the only node
// that ships the app's drains. A nil opener means local; the returned close
// releases the connection.
func logDrainHeaderOpenerFromEnv(ctx context.Context, getenv func(string) string, nodeName string,
	log *slog.Logger) (logDrainHeaderOpener, func(), error) {
	switch mode := getenv("FAAS_GATEWAY_LOG_DRAIN_OPENER"); mode {
	case "", "local":
		return nil, func() {}, nil
	case "secretd":
	default:
		return nil, nil, fmt.Errorf("gatewayd-internal: FAAS_GATEWAY_LOG_DRAIN_OPENER=%q: want local or secretd", mode)
	}
	if nodeName == "" {
		return nil, nil, errors.New("gatewayd-internal: FAAS_GATEWAY_LOG_DRAIN_OPENER=secretd is for a compute node (FAAS_NODE_NAME): secretd answers the app's owner node")
	}
	target := getenv("FAAS_GATEWAY_SECRETD_TARGET")
	parsed, err := wire.ParseTarget(target)
	if target == "" || err != nil || parsed.Scheme == wire.SchemeUnix {
		return nil, nil, fmt.Errorf("gatewayd-internal: FAAS_GATEWAY_SECRETD_TARGET=%q: want a tcp:// or dns:// target on the control plane", target)
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("secretd_",
		getenv("FAAS_SECRETD_TLS_CERT_PATH"), getenv("FAAS_SECRETD_TLS_KEY_PATH"), getenv("FAAS_SECRETD_TLS_CA_PATH"))
	if err != nil {
		return nil, nil, fmt.Errorf("gatewayd-internal: load secretd client TLS: %w", err)
	}
	if tlsCfg == nil {
		return nil, nil, errors.New("gatewayd-internal: FAAS_GATEWAY_LOG_DRAIN_OPENER=secretd needs FAAS_SECRETD_TLS_{CERT,KEY,CA}_PATH")
	}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = secretdServerName
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("gatewayd-internal: dial secretd: %w", err)
	}
	log.Info("gatewayd-internal: log drain headers opened by secretd", "target", target)
	return secretdLogDrainHeaders(secretdpb.NewSecretdClient(conn)), func() {
		if err := conn.Close(); err != nil {
			log.Warn("gatewayd-internal: close secretd client", "err", err)
		}
	}, nil
}

// secretdLogDrainHeaders is a logDrainHeaderOpener over secretd.
func secretdLogDrainHeaders(rpc secretdpb.SecretdClient) logDrainHeaderOpener {
	return func(ctx context.Context, appID, drainID string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, secretdLogDrainOpenTimeout)
		defer cancel()
		resp, err := rpc.OpenLogDrainHeader(ctx, &secretdpb.OpenLogDrainHeaderRequest{AppId: appID, DrainId: drainID})
		if err != nil {
			return "", fmt.Errorf("secretd: %w", err)
		}
		return resp.GetHeader(), nil
	}
}
