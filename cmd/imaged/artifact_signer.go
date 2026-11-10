package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	signdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/signd/v1"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/signd"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// signdServerName is pki.SigndServerName, the SAN on signd's server leaf.
const signdServerName = "signd.faas"

// artifactSignerFromEnv picks who signs the layers imaged publishes
// (ADR-20261109170000 §5 step 2). FAAS_ARTIFACT_SIGNER unset or "local" keeps
// the node's own key. "signd" sends app layers to the control plane's signd
// at FAAS_SIGND_TARGET over mTLS with the node's identity leaf; local still
// signs the runtime bases while the node has the key. The returned close
// releases the connection.
func artifactSignerFromEnv(ctx context.Context, getenv func(string) string, local rootfs.Signer,
	objects storage.StorageBackend, log *slog.Logger) (rootfs.Signer, func(), error) {
	switch mode := getenv("FAAS_ARTIFACT_SIGNER"); mode {
	case "", "local":
		return local, func() {}, nil
	case "signd":
	default:
		return nil, nil, fmt.Errorf("imaged: FAAS_ARTIFACT_SIGNER=%q: want local or signd", mode)
	}
	target := getenv("FAAS_SIGND_TARGET")
	if target == "" {
		return nil, nil, errors.New("imaged: FAAS_ARTIFACT_SIGNER=signd needs FAAS_SIGND_TARGET")
	}
	parsed, err := wire.ParseTarget(target)
	if err != nil || parsed.Scheme == wire.SchemeUnix {
		return nil, nil, fmt.Errorf("imaged: FAAS_SIGND_TARGET=%q: want a tcp:// or dns:// target on the control plane", target)
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("signd_",
		getenv("FAAS_SIGND_TLS_CERT_PATH"), getenv("FAAS_SIGND_TLS_KEY_PATH"), getenv("FAAS_SIGND_TLS_CA_PATH"))
	if err != nil {
		return nil, nil, fmt.Errorf("imaged: load signd client TLS: %w", err)
	}
	if tlsCfg == nil {
		return nil, nil, errors.New("imaged: FAAS_ARTIFACT_SIGNER=signd needs FAAS_SIGND_TLS_{CERT,KEY,CA}_PATH")
	}
	// signd's leaf names only signd.faas (pkg/pki FeatureArtifactSigner): a
	// leaf of another daemon from the same CA does not pass as signd, and
	// dialing the control plane by address still verifies.
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = signdServerName
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("imaged: dial signd: %w", err)
	}
	log.Info("imaged: app layers signed by signd", "target", target)
	return signd.NewClient(signdpb.NewSigndClient(conn), objects, local), func() {
		if err := conn.Close(); err != nil {
			log.Warn("imaged: close signd client", "err", err)
		}
	}, nil
}
