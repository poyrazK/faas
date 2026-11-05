// Node binding for in-guest span and log exports (ADR-958 amendment
// 2026-10-28, the binding §4 deferred).
//
// vmmd supplies each frame's account, app, deployment and instance. On the
// shared mutual-TLS listener apid accepts a frame only when that instance
// belongs to the app, deployment and account it names and runs on the node
// the caller's certificate names, or ran there before it migrated. No fleet
// certificate can then write another node's apps' traces or logs. A local
// socket carries no certificate: vmmd on the same box, trusted as before.
// RuntimeLogEventsReceiver binds runtime lines the same way.

package receivers

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// GuestBindingStore is the Store subset the binding reads.
type GuestBindingStore interface {
	AppByID(ctx context.Context, id string) (state.App, error)
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
}

// Decisions are cached so an exporting instance costs no database read per
// frame. A refusal is kept briefly: a migrating instance's new node may
// export before its row names that node.
const (
	guestBindingOwnTTL     = time.Minute
	guestBindingForeignTTL = 5 * time.Second
	guestBindingMaxEntries = 50000
)

type guestBinding struct {
	nodes wire.NodeIdentityResolver
	store GuestBindingStore
	log   *slog.Logger
	now   func() time.Time

	mu      sync.Mutex
	decided map[guestBindingKey]guestBindingDecision
}

type guestBindingKey struct {
	node, account, app, deployment, instance string
}

type guestBindingDecision struct {
	own   bool
	until time.Time
}

// WithPeerNodes binds mutual-TLS callers to the compute node their
// certificate names (multi-box apid's node verifier), reading instances and
// apps from store. Without it every frame is accepted as before.
func (g *GuestSpansIngester) WithPeerNodes(nodes wire.NodeIdentityResolver, store GuestBindingStore) *GuestSpansIngester {
	if g != nil && nodes != nil && store != nil {
		g.binding = &guestBinding{nodes: nodes, store: store, log: g.log, now: time.Now, decided: map[guestBindingKey]guestBindingDecision{}}
	}
	return g
}

// bindToPeerNode returns "" when the frame may be ingested, or the outcome
// that refuses it: invalid for another node's or an unknown instance (the
// SDK must not retry), unavailable when the lookup failed. The error is set
// when the caller's certificate names no registered node.
func (g *GuestSpansIngester) bindToPeerNode(ctx context.Context, req *apidpb.IngestGuestSpansRequest) (string, error) {
	if g.binding == nil {
		return "", nil
	}
	cn, err := wire.PeerCN(ctx)
	if errors.Is(err, wire.ErrPeerCNUnavailable) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	node, err := g.binding.nodes.NodeIDByCN(cn)
	if err != nil {
		return "", err
	}
	key := guestBindingKey{node: node, account: req.GetAccountId(), app: req.GetAppId(), deployment: req.GetDeploymentId(), instance: req.GetInstanceId()}
	own, err := g.binding.owns(ctx, key)
	switch {
	case err != nil:
		g.log.Warn("guest ingest: instance lookup failed", "instance_id", key.instance, "err", err)
		return guestSpansUnavailable, nil
	case !own:
		return guestSpansInvalid, nil
	}
	return "", nil
}

func (b *guestBinding) owns(ctx context.Context, key guestBindingKey) (bool, error) {
	now := b.now()
	b.mu.Lock()
	decision, seen := b.decided[key]
	b.mu.Unlock()
	if seen && now.Before(decision.until) {
		return decision.own, nil
	}
	own, err := b.lookup(ctx, key)
	if err != nil {
		return false, err
	}
	ttl := guestBindingOwnTTL
	if !own {
		ttl = guestBindingForeignTTL
		b.log.Warn("guest ingest: refused a frame for an instance the caller's node does not run",
			"node_id", key.node, "instance_id", key.instance, "app_id", key.app)
	}
	b.mu.Lock()
	if len(b.decided) >= guestBindingMaxEntries {
		b.evictExpired(now)
	}
	b.decided[key] = guestBindingDecision{own: own, until: now.Add(ttl)}
	b.mu.Unlock()
	return own, nil
}

// evictExpired drops stale decisions, or all of them when none has expired,
// so the cache stays bounded. Callers hold mu.
func (b *guestBinding) evictExpired(now time.Time) {
	for key, decision := range b.decided {
		if !now.Before(decision.until) {
			delete(b.decided, key)
		}
	}
	if len(b.decided) >= guestBindingMaxEntries {
		clear(b.decided)
	}
}

// lookup reports whether the instance belongs to the frame's app and
// deployment, runs on the node (or ran there before it migrated), and the
// app to the frame's account. An unknown instance or app is not the caller's.
func (b *guestBinding) lookup(ctx context.Context, key guestBindingKey) (bool, error) {
	if !isUUID(key.instance) || !isUUID(key.app) {
		return false, nil
	}
	instanceByID := b.store.InstanceByID
	if m, ok := b.store.(migrationInstanceStore); ok {
		instanceByID = m.MigrationInstanceByID
	}
	inst, err := instanceByID(ctx, key.instance)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	onNode := inst.NodeID == key.node || inst.MigratedFromNodeID != nil && *inst.MigratedFromNodeID == key.node
	if !onNode || inst.AppID != key.app || inst.DeploymentID != key.deployment {
		return false, nil
	}
	app, err := b.store.AppByID(ctx, key.app)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return app.AccountID == key.account, nil
}
