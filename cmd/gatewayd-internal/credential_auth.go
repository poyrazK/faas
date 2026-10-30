package main

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/onebox-faas/faas/pkg/apidgrpc"
)

// Credential hashes are keyed by a secret only apid holds (ADR
// 20261029202403), so gatewayd-internal asks apid to resolve API keys and
// OIDC bearers instead of reading api_keys itself. The key cache in front
// of require_authn keeps the hop off the hot path.

// credentialAuthTarget is where apid answers credential lookups: the
// split-box mTLS listener gatewayd-internal reports app errors to, or apid's
// local auth socket on a single box.
func credentialAuthTarget(cfg *Config, getenv func(string) string) (string, *tls.Config, error) {
	target := cfg.GetAppErrorsTarget(getenv)
	if isUnixSocketPath(target) {
		if socket := getenv("FAAS_APID_AUTH_SOCKET"); socket != "" {
			return socket, nil, nil
		}
		return "/run/faas/auth.sock", nil, nil
	}
	tlsCfg, err := cfg.LoadAppErrorsTLS()
	if err != nil {
		return "", nil, fmt.Errorf("credential auth TLS: %w", err)
	}
	if tlsCfg == nil {
		return "", nil, fmt.Errorf("credential auth: target %q is not a unix socket but app_errors_tls_* is empty (mTLS is required)", target)
	}
	return target, tlsCfg, nil
}

// dialCredentialAuth opens the lazy apid connection credential lookups use.
func dialCredentialAuth(ctx context.Context, cfg *Config, getenv func(string) string) (*apidgrpc.AuthClientImpl, error) {
	target, tlsCfg, err := credentialAuthTarget(cfg, getenv)
	if err != nil {
		return nil, err
	}
	return apidgrpc.DialAuth(ctx, target, tlsCfg)
}
