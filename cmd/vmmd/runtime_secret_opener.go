package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"filippo.io/age"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
)

// secretdServerName is pki.SecretdServerName, the SAN on secretd's server
// leaf.
const secretdServerName = "secretd.faas"

// secretdOpenTimeout bounds one open. A wake that carries secrets waits on
// it, so it is short; secretd answers from one row lookup and the opens.
const secretdOpenTimeout = 5 * time.Second

// runtimeSecretOpenerFromEnv picks who opens app secrets
// (ADR-20261110210000 §3, slice 3c). FAAS_RUNTIME_SECRET_OPENER unset or
// "local" opens them with this node's runtime identities. "secretd" asks the
// control plane's secretd at FAAS_SECRETD_TARGET over mTLS with the node's
// identity leaf; secretd opens only for the app's owner node or a granted
// peer, and refuses unbound envelopes, so it needs requireBinding. A nil
// opener means local. The returned close releases the connection.
func runtimeSecretOpenerFromEnv(ctx context.Context, getenv func(string) string, requireBinding bool,
	log *slog.Logger) (fcvm.RuntimeSecretOpener, func(), error) {
	switch mode := getenv("FAAS_RUNTIME_SECRET_OPENER"); mode {
	case "", "local":
		return nil, func() {}, nil
	case "secretd":
	default:
		return nil, nil, fmt.Errorf("vmmd: FAAS_RUNTIME_SECRET_OPENER=%q: want local or secretd", mode)
	}
	if !requireBinding {
		return nil, nil, errors.New("vmmd: FAAS_RUNTIME_SECRET_OPENER=secretd needs runtime secret binding required " +
			"(faas_seal_domain_runtime_binding_required): secretd refuses unbound envelopes")
	}
	target := getenv("FAAS_SECRETD_TARGET")
	if target == "" {
		return nil, nil, errors.New("vmmd: FAAS_RUNTIME_SECRET_OPENER=secretd needs FAAS_SECRETD_TARGET")
	}
	parsed, err := wire.ParseTarget(target)
	if err != nil || parsed.Scheme == wire.SchemeUnix {
		return nil, nil, fmt.Errorf("vmmd: FAAS_SECRETD_TARGET=%q: want a tcp:// or dns:// target on the control plane", target)
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("secretd_",
		getenv("FAAS_SECRETD_TLS_CERT_PATH"), getenv("FAAS_SECRETD_TLS_KEY_PATH"), getenv("FAAS_SECRETD_TLS_CA_PATH"))
	if err != nil {
		return nil, nil, fmt.Errorf("vmmd: load secretd client TLS: %w", err)
	}
	if tlsCfg == nil {
		return nil, nil, errors.New("vmmd: FAAS_RUNTIME_SECRET_OPENER=secretd needs FAAS_SECRETD_TLS_{CERT,KEY,CA}_PATH")
	}
	// secretd's leaf names only secretd.faas (pkg/pki FeatureSecretOpener): a
	// leaf of another daemon from the same CA does not pass as secretd.
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = secretdServerName
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("vmmd: dial secretd: %w", err)
	}
	log.Info("vmmd: app secrets opened by secretd", "target", target)
	return secretdOpener{rpc: secretdpb.NewSecretdClient(conn)}, func() {
		if err := conn.Close(); err != nil {
			log.Warn("vmmd: close secretd client", "err", err)
		}
	}, nil
}

// secretdOpener is fcvm.RuntimeSecretOpener over secretd.
type secretdOpener struct {
	rpc secretdpb.SecretdClient
}

func (o secretdOpener) OpenAppSecrets(ctx context.Context, appID string, entries []fcvm.SealedEnvEntry) (map[string]string, error) {
	req := &secretdpb.OpenAppSecretsRequest{AppId: appID, Entries: make([]*secretdpb.SealedEntry, 0, len(entries))}
	for _, e := range entries {
		req.Entries = append(req.Entries, &secretdpb.SealedEntry{Key: e.Key, Ciphertext: e.Ciphertext, SourceKey: e.SourceKey})
	}
	ctx, cancel := context.WithTimeout(ctx, secretdOpenTimeout)
	defer cancel()
	resp, err := o.rpc.OpenAppSecrets(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("secretd: %w", err)
	}
	return resp.GetValues(), nil
}

// OpenCaptureKey implements fcvm.CaptureKeyOpener: secretd reads the sealed
// key from the capture's row (ADR-20261110210000 slice 3d).
func (o secretdOpener) OpenCaptureKey(ctx context.Context, appID, captureID string) (*age.X25519Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, secretdOpenTimeout)
	defer cancel()
	resp, err := o.rpc.OpenCaptureKey(ctx, &secretdpb.OpenCaptureKeyRequest{AppId: appID, CaptureId: captureID})
	if err != nil {
		return nil, fmt.Errorf("secretd: %w", err)
	}
	id, err := age.ParseX25519Identity(resp.GetIdentity())
	if err != nil {
		return nil, fmt.Errorf("secretd: capture key: %w", err)
	}
	return id, nil
}

var _ fcvm.CaptureKeyOpener = secretdOpener{}
