package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

// secretdServerName is pki.SecretdServerName, the SAN on secretd's server
// leaf.
const secretdServerName = "secretd.faas"

// secretGrantRPCFromEnv returns secretd's GrantPeer for this node's schedd
// (ADR-20261110210000 §3 slice 3c-2), or nil while FAAS_SCHEDD_SECRETD_TARGET
// is unset. schedd dials secretd with the node's secretd client leaf (the
// one vmmd uses, handed to schedd as a credential), so secretd knows the
// caller as the app's owner node. The returned close releases the
// connection.
func secretGrantRPCFromEnv(ctx context.Context, getenv func(string) string, ownerNodeID string,
	log *slog.Logger) (sched.SecretGrantRPC, func(), error) {
	target := getenv("FAAS_SCHEDD_SECRETD_TARGET")
	if target == "" {
		return nil, func() {}, nil
	}
	if ownerNodeID == "" {
		return nil, nil, errors.New("schedd: FAAS_SCHEDD_SECRETD_TARGET is for a compute node's schedd, which owns apps; this schedd has no node")
	}
	parsed, err := wire.ParseTarget(target)
	if err != nil || parsed.Scheme == wire.SchemeUnix {
		return nil, nil, fmt.Errorf("schedd: FAAS_SCHEDD_SECRETD_TARGET=%q: want a tcp:// or dns:// target on the control plane", target)
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("secretd_",
		getenv("FAAS_SECRETD_TLS_CERT_PATH"), getenv("FAAS_SECRETD_TLS_KEY_PATH"), getenv("FAAS_SECRETD_TLS_CA_PATH"))
	if err != nil {
		return nil, nil, fmt.Errorf("schedd: load secretd client TLS: %w", err)
	}
	if tlsCfg == nil {
		return nil, nil, errors.New("schedd: FAAS_SCHEDD_SECRETD_TARGET needs FAAS_SECRETD_TLS_{CERT,KEY,CA}_PATH")
	}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = secretdServerName
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("schedd: dial secretd: %w", err)
	}
	log.Info("schedd: peers granted app secrets through secretd", "target", target)
	return secretdGrants{rpc: secretdpb.NewSecretdClient(conn)}, func() {
		if err := conn.Close(); err != nil {
			log.Warn("schedd: close secretd client", "err", err)
		}
	}, nil
}

// secretdGrants is sched.SecretGrantRPC over secretd.
type secretdGrants struct {
	rpc secretdpb.SecretdClient
}

func (s secretdGrants) GrantPeer(ctx context.Context, appID, nodeName string, ttl time.Duration) (time.Time, error) {
	resp, err := s.rpc.GrantPeer(ctx, &secretdpb.GrantPeerRequest{AppId: appID, NodeName: nodeName, TtlSeconds: int64(ttl / time.Second)})
	if err != nil {
		return time.Time{}, fmt.Errorf("secretd: %w", err)
	}
	exp, err := time.Parse(time.RFC3339Nano, resp.GetExpiresAt())
	if err != nil {
		return time.Time{}, fmt.Errorf("secretd: grant expiry %q: %w", resp.GetExpiresAt(), err)
	}
	return exp, nil
}

// appRunsOn reports whether the ledger holds a live instance of appID on
// nodeID.
func appRunsOn(ledger *sched.NodeLedger) func(appID, nodeID string) bool {
	return func(appID, nodeID string) bool {
		return ledger != nil && ledger.AppInstancesByNode(appID)[nodeID] > 0
	}
}
