package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/wire"
)

// secretdVerificationSecrets opens a verification contract's header secret
// references through secretd (ADR-20261122220000 amendment 2).
type secretdVerificationSecrets struct{ client secretdpb.SecretdClient }

func (s secretdVerificationSecrets) OpenVerificationSecrets(ctx context.Context, appID, deploymentID string, keys []string) (map[string]string, []string, error) {
	resp, err := s.client.OpenVerificationSecrets(ctx, &secretdpb.OpenVerificationSecretsRequest{AppId: appID, DeploymentId: deploymentID, Keys: keys})
	if err != nil {
		return nil, nil, err
	}
	return resp.GetValues(), resp.GetMissing(), nil
}

// verificationSecretsFromEnv dials secretd at FAAS_SECRETD_TARGET with the
// node's secretd client leaf. Without a target it returns nil: a contract
// that references secrets then cannot run on this node and its deployment
// waits out the hosting verification recovery window.
func verificationSecretsFromEnv(ctx context.Context, getenv func(string) string, log *slog.Logger) (imaged.VerificationSecretOpener, func(), error) {
	target := getenv("FAAS_SECRETD_TARGET")
	if target == "" {
		return nil, func() {}, nil
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
		return nil, nil, errors.New("imaged: FAAS_SECRETD_TARGET needs FAAS_SECRETD_TLS_{CERT,KEY,CA}_PATH")
	}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = secretdServerName
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("imaged: dial secretd: %w", err)
	}
	return secretdVerificationSecrets{client: secretdpb.NewSecretdClient(conn)}, func() {
		if err := conn.Close(); err != nil {
			log.Warn("imaged: close secretd verification client", "err", err)
		}
	}, nil
}
