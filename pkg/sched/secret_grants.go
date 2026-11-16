package sched

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ADR-20261110210000 §3 slice 3c-2: secretd opens an app's secrets for the
// app's owner node and for a peer the owner granted. This schedd is the
// owner of the apps it wakes; before it places an instance on another node it
// grants that node, and while the instance runs there it renews the grant, so
// the peer's vmmd can open the app's secrets (at wake and on live reload).

// SecretGrantRPC is secretd's GrantPeer, called with this node's identity.
type SecretGrantRPC interface {
	GrantPeer(ctx context.Context, appID, nodeName string, ttl time.Duration) (time.Time, error)
}

// SecretGrantTTL is how long one grant lasts; secretd clamps it to 1 h.
// The renew loop re-grants every SecretGrantRenewInterval, well inside it.
const (
	SecretGrantTTL           = time.Hour
	SecretGrantRenewInterval = 15 * time.Minute
	secretGrantTimeout       = 3 * time.Second
)

type secretGrantKey struct{ appID, nodeID string }

// SecretGrantor issues and renews this owner's peer grants.
type SecretGrantor struct {
	rpc       SecretGrantRPC
	ownNodeID string
	// nodeName maps a compute_nodes.id to its name, the CN secretd knows
	// the peer by.
	nodeName func(nodeID string) (string, bool)
	// live reports whether the app still has an instance on the node.
	live func(appID, nodeID string) bool
	now  func() time.Time
	log  *slog.Logger

	mu sync.Mutex
	// expires is each granted pair's expiry as secretd reported it.
	expires map[secretGrantKey]time.Time
}

// NewSecretGrantor returns a grantor for the owner node ownNodeID.
func NewSecretGrantor(rpc SecretGrantRPC, ownNodeID string, nodeName func(string) (string, bool),
	live func(appID, nodeID string) bool, log *slog.Logger) *SecretGrantor {
	if log == nil {
		log = slog.Default()
	}
	return &SecretGrantor{rpc: rpc, ownNodeID: ownNodeID, nodeName: nodeName, live: live,
		now: time.Now, log: log, expires: map[secretGrantKey]time.Time{}}
}

// Ensure grants nodeID the app's secrets before an instance is placed there.
// A grant with more than half its lifetime left is reused. Failure is
// logged, not returned: a peer that still opens secrets itself needs no
// grant, and one that asks secretd is refused with a clear error.
func (g *SecretGrantor) Ensure(ctx context.Context, nodeID, appID string) {
	if g == nil || g.rpc == nil || appID == "" || nodeID == "" || nodeID == g.ownNodeID {
		return
	}
	key := secretGrantKey{appID, nodeID}
	g.mu.Lock()
	exp, ok := g.expires[key]
	g.mu.Unlock()
	if ok && exp.Sub(g.now()) > SecretGrantTTL/2 {
		return
	}
	g.grant(ctx, key)
}

func (g *SecretGrantor) grant(ctx context.Context, key secretGrantKey) {
	name, ok := g.nodeName(key.nodeID)
	if !ok || name == "" {
		g.log.Warn("sched: secret grant: peer has no node name", "app_id", key.appID, "node_id", key.nodeID)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, secretGrantTimeout)
	defer cancel()
	exp, err := g.rpc.GrantPeer(ctx, key.appID, name, SecretGrantTTL)
	if err != nil {
		g.log.Warn("sched: secret grant refused or failed", "app_id", key.appID, "node", name, "err", err)
		return
	}
	g.mu.Lock()
	g.expires[key] = exp
	g.mu.Unlock()
}

// Renew re-grants every pair whose app still runs on the peer and forgets
// the rest; an unrenewed grant expires on its own within SecretGrantTTL.
func (g *SecretGrantor) Renew(ctx context.Context) {
	if g == nil || g.rpc == nil {
		return
	}
	g.mu.Lock()
	keys := make([]secretGrantKey, 0, len(g.expires))
	for key := range g.expires {
		keys = append(keys, key)
	}
	g.mu.Unlock()
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		if g.live != nil && !g.live(key.appID, key.nodeID) {
			g.mu.Lock()
			delete(g.expires, key)
			g.mu.Unlock()
			continue
		}
		g.grant(ctx, key)
	}
}

// Run renews grants until ctx ends.
func (g *SecretGrantor) Run(ctx context.Context) {
	t := time.NewTicker(SecretGrantRenewInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.Renew(ctx)
		}
	}
}
