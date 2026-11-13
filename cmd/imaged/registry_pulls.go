package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/registryproxy"
	"github.com/onebox-faas/faas/pkg/wire"
)

// secretdServerName is pki.SecretdServerName, the SAN on secretd's server
// leaf.
const secretdServerName = "secretd.faas"

// registryProxyFromEnv (ADR-20261110210000 slice 3j-2):
// FAAS_IMAGED_REGISTRY_PULLS unset or "local" opens private-registry
// passwords here with the build identity; "secretd" pulls private images
// through the control plane's secretd at FAAS_SECRETD_TARGET with the node's
// secretd client leaf, so this node never holds an app's registry password.
// A nil proxy means local. The returned close releases the connection.
func registryProxyFromEnv(ctx context.Context, getenv func(string) string, log *slog.Logger) (*registryproxy.Proxy, func(), error) {
	switch mode := getenv("FAAS_IMAGED_REGISTRY_PULLS"); mode {
	case "", "local":
		return nil, func() {}, nil
	case "secretd":
	default:
		return nil, nil, fmt.Errorf("imaged: FAAS_IMAGED_REGISTRY_PULLS=%q: want local or secretd", mode)
	}
	target := getenv("FAAS_SECRETD_TARGET")
	if target == "" {
		return nil, nil, errors.New("imaged: FAAS_IMAGED_REGISTRY_PULLS=secretd needs FAAS_SECRETD_TARGET")
	}
	parsed, err := wire.ParseTarget(target)
	if err != nil || parsed.Scheme == wire.SchemeUnix {
		return nil, nil, fmt.Errorf("imaged: FAAS_SECRETD_TARGET=%q: want a tcp:// or dns:// target on the control plane", target)
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("secretd_",
		getenv("FAAS_SECRETD_TLS_CERT_PATH"), getenv("FAAS_SECRETD_TLS_KEY_PATH"), getenv("FAAS_SECRETD_TLS_CA_PATH"))
	if err != nil {
		return nil, nil, fmt.Errorf("imaged: load secretd client TLS: %w", err)
	}
	if tlsCfg == nil {
		return nil, nil, errors.New("imaged: FAAS_IMAGED_REGISTRY_PULLS=secretd needs FAAS_SECRETD_TLS_{CERT,KEY,CA}_PATH")
	}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = secretdServerName
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("imaged: dial secretd: %w", err)
	}
	log.Info("imaged: private images pulled through secretd", "target", target)
	return &registryproxy.Proxy{Client: secretdpb.NewSecretdClient(conn)}, func() {
		if err := conn.Close(); err != nil {
			log.Warn("imaged: close secretd client", "err", err)
		}
	}, nil
}
