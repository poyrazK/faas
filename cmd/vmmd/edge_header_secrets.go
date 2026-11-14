package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// edgeHeaderSecretTTL is how long an opened edge-rule header value is reused
// for an app. Rows never change; the bound is for authorization: an app that
// moved away from this node stops getting values within it.
const edgeHeaderSecretTTL = 5 * time.Minute

// edgeHeaderSecretCacheMax bounds the values kept; past it the cache starts
// over.
const edgeHeaderSecretCacheMax = 10_000

// edgeHeaderSecretOpenerFromEnv (ADR-20261110210000 slice 3i):
// FAAS_VMMD_EDGE_HEADER_SECRETS unset or "off" leaves forwards that name a
// sealed edge-rule header refused; "secretd" opens them through the control
// plane's secretd as this node, naming the instance's app. Gateways send
// such forwards only with FAAS_GATEWAY_EDGE_HEADER_SECRETS=inject, which
// must not be turned on before every node's vmmd runs with "secretd".
func edgeHeaderSecretOpenerFromEnv(ctx context.Context, getenv func(string) string,
	appOf func(instance string) (string, error), log *slog.Logger) (vmmdgrpc.EdgeHeaderSecretOpener, func(), error) {
	switch mode := getenv("FAAS_VMMD_EDGE_HEADER_SECRETS"); mode {
	case "", "off":
		return nil, func() {}, nil
	case "secretd":
	default:
		return nil, nil, fmt.Errorf("vmmd: FAAS_VMMD_EDGE_HEADER_SECRETS=%q: want off or secretd", mode)
	}
	rpc, closeConn, err := dialSecretd(ctx, getenv, "FAAS_VMMD_EDGE_HEADER_SECRETS", log)
	if err != nil {
		return nil, nil, err
	}
	log.Info("vmmd: edge rule header values opened by secretd")
	return newEdgeHeaderSecretOpener(rpc, appOf, time.Now), closeConn, nil
}

type edgeHeaderSecretKey struct{ target, app, id string }

type edgeHeaderSecretEntry struct {
	value   string
	expires time.Time
}

// edgeHeaderSecretOpener is vmmdgrpc.EdgeHeaderSecretOpener over secretd.
type edgeHeaderSecretOpener struct {
	rpc   secretdpb.SecretdClient
	appOf func(instance string) (string, error)
	now   func() time.Time

	mu    sync.Mutex
	cache map[edgeHeaderSecretKey]edgeHeaderSecretEntry
}

func newEdgeHeaderSecretOpener(rpc secretdpb.SecretdClient, appOf func(string) (string, error), now func() time.Time) *edgeHeaderSecretOpener {
	return &edgeHeaderSecretOpener{rpc: rpc, appOf: appOf, now: now, cache: map[edgeHeaderSecretKey]edgeHeaderSecretEntry{}}
}

// OpenEdgeHeaderSecret implements vmmdgrpc.EdgeHeaderSecretOpener. The
// target app is the instance's, read from this node's live instance table,
// never from the request.
func (o *edgeHeaderSecretOpener) OpenEdgeHeaderSecret(ctx context.Context, instance, secretAppID, secretID string) (string, error) {
	target, err := o.appOf(instance)
	if err != nil {
		return "", err
	}
	key := edgeHeaderSecretKey{target: target, app: secretAppID, id: secretID}
	now := o.now()
	o.mu.Lock()
	entry, ok := o.cache[key]
	o.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.value, nil
	}
	ctx, cancel := context.WithTimeout(ctx, secretdOpenTimeout)
	defer cancel()
	resp, err := o.rpc.OpenEdgeHeaderSecret(ctx, &secretdpb.OpenEdgeHeaderSecretRequest{
		TargetAppId: target, SecretAppId: secretAppID, SecretId: secretID})
	if err != nil {
		o.mu.Lock()
		delete(o.cache, key)
		o.mu.Unlock()
		return "", fmt.Errorf("secretd: %w", err)
	}
	o.mu.Lock()
	if len(o.cache) >= edgeHeaderSecretCacheMax {
		o.cache = map[edgeHeaderSecretKey]edgeHeaderSecretEntry{}
	}
	o.cache[key] = edgeHeaderSecretEntry{value: resp.GetValue(), expires: now.Add(edgeHeaderSecretTTL)}
	o.mu.Unlock()
	return resp.GetValue(), nil
}
