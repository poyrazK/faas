// Package secretd is the control plane's opener of customer runtime secrets
// (ADR-20261110210000 §3). Compute nodes hold no runtime seal-domain
// identity; a node's vmmd asks secretd, over mutual TLS, to open the sealed
// entries of an app it runs. secretd answers only:
//
//   - the app's owner node: apps.node_id, which only the control plane may
//     set once app_owner_authority is enforce (ADR §4, slice 1a), or
//   - a peer the owner granted (secret_grants, written only here, after
//     checking that the grantor is the owner).
//
// Every envelope must be bound to the app's resource account, every open is
// recorded in secret_open_audit before any plaintext leaves, and a node that
// opens more distinct apps per hour than its budget is refused.
package secretd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/wire"
)

// AppTarget is what secretd needs to know about an app.
type AppTarget struct {
	// OwnerNodeID is apps.node_id; empty while the app is unplaced.
	OwnerNodeID string
	// AccountID is the app's resource account (state.AppResourceAccount),
	// the account its envelopes are bound to.
	AccountID string
}

// Store is secretd's view of the database.
type Store interface {
	// NodeIDByName maps an mTLS certificate name to compute_nodes.id. It
	// returns ErrUnknownNode for a name with no active node.
	NodeIDByName(ctx context.Context, name string) (string, error)
	AppTarget(ctx context.Context, appID string) (AppTarget, error)
	GrantValid(ctx context.Context, appID, nodeID string, at time.Time) (bool, error)
	UpsertGrant(ctx context.Context, appID, nodeID, grantedByNodeID string, expiresAt time.Time) (time.Time, error)
	RecordOpen(ctx context.Context, nodeID, appID, via string, entries int) error
}

// ErrUnknownNode is returned by Store.NodeIDByName.
var ErrUnknownNode = errors.New("secretd: no active compute node has that name")

// Limits bound what one node may do.
const (
	// DefaultAppsPerNodeHour is how many distinct apps one node may open in a
	// rolling hour. A node at the RAM admission ceiling with the smallest
	// plan holds a few hundred instances; churn through more than this in an
	// hour is a node collecting secrets, not running apps.
	DefaultAppsPerNodeHour = 1000
	// MaxEntriesPerRequest bounds one request.
	MaxEntriesPerRequest = 512
	// MinGrantTTL and MaxGrantTTL clamp a grant's lifetime; the owner renews
	// it while the peer runs the app.
	MinGrantTTL = time.Minute
	MaxGrantTTL = time.Hour
)

// Server implements secretdpb.SecretdServer.
type Server struct {
	secretdpb.UnimplementedSecretdServer
	store      Store
	identities []*age.X25519Identity
	log        *slog.Logger
	// peerNode names the calling node; wire.PeerCN in production.
	peerNode func(context.Context) (string, error)
	now      func() time.Time
	budget   *appBudget
}

// NewServer returns a server that opens with identities, the runtime seal
// domain's (current first, then previous).
func NewServer(store Store, identities []*age.X25519Identity, appsPerNodeHour int, log *slog.Logger) *Server {
	if appsPerNodeHour <= 0 {
		appsPerNodeHour = DefaultAppsPerNodeHour
	}
	return &Server{
		store: store, identities: identities, log: log,
		peerNode: wire.PeerCN, now: time.Now,
		budget: newAppBudget(appsPerNodeHour, time.Hour),
	}
}

// WithPeerNode replaces the caller identity source (tests).
func (s *Server) WithPeerNode(f func(context.Context) (string, error)) *Server {
	s.peerNode = f
	return s
}

// WithClock replaces the clock (tests).
func (s *Server) WithClock(now func() time.Time) *Server {
	s.now = now
	return s
}

func rpcErr(code codes.Code, msg string) error { return status.Error(code, msg) }

// caller returns the calling node's name and id.
func (s *Server) caller(ctx context.Context) (string, string, error) {
	name, err := s.peerNode(ctx)
	if err != nil || strings.TrimSpace(name) == "" {
		return "", "", rpcErr(codes.Unauthenticated, "secretd refused the caller: no node identity")
	}
	id, err := s.store.NodeIDByName(ctx, name)
	if errors.Is(err, ErrUnknownNode) {
		return "", "", rpcErr(codes.PermissionDenied, "secretd refused the caller: not an active compute node")
	}
	if err != nil {
		s.log.Error("secretd: node lookup", "node", logsanitize.Field(name), "err", err)
		return "", "", rpcErr(codes.Unavailable, "secretd could not look up the caller")
	}
	return name, id, nil
}

// OpenAppSecrets implements secretdpb.SecretdServer.
func (s *Server) OpenAppSecrets(ctx context.Context, req *secretdpb.OpenAppSecretsRequest) (*secretdpb.OpenAppSecretsResponse, error) {
	name, nodeID, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	appID := req.GetAppId()
	if appID == "" || len(req.GetEntries()) > MaxEntriesPerRequest {
		return nil, rpcErr(codes.InvalidArgument, "secretd needs an app id and at most 512 entries")
	}
	target, err := s.store.AppTarget(ctx, appID)
	if err != nil {
		s.log.Warn("secretd: app lookup", "app_id", logsanitize.Field(appID), "err", err)
		return nil, rpcErr(codes.NotFound, "secretd found no such app")
	}
	via, err := s.authorize(ctx, appID, nodeID, target)
	if err != nil {
		s.log.Warn("secretd: refused open", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID), "err", err)
		return nil, rpcErr(codes.PermissionDenied, "secretd refused: the caller neither owns the app nor holds a grant for it")
	}
	if !s.budget.allow(nodeID, appID, s.now()) {
		s.log.Error("secretd: node over its distinct-app budget", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID))
		return nil, rpcErr(codes.ResourceExhausted, "secretd refused: the caller opened too many distinct apps this hour")
	}
	values, err := s.open(req.GetEntries(), target.AccountID)
	if err != nil {
		s.log.Warn("secretd: refused envelope", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID), "err", err)
		return nil, rpcErr(codes.FailedPrecondition, "secretd refused an envelope: "+err.Error())
	}
	// The record precedes the plaintext: an open that cannot be recorded
	// does not happen.
	if err := s.store.RecordOpen(ctx, nodeID, appID, via, len(values)); err != nil {
		clear(values)
		s.log.Error("secretd: record open", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID), "err", err)
		return nil, rpcErr(codes.Unavailable, "secretd could not record the open")
	}
	return &secretdpb.OpenAppSecretsResponse{Values: values}, nil
}

func (s *Server) authorize(ctx context.Context, appID, nodeID string, target AppTarget) (string, error) {
	if target.OwnerNodeID != "" && target.OwnerNodeID == nodeID {
		return "owner", nil
	}
	ok, err := s.store.GrantValid(ctx, appID, nodeID, s.now())
	if err != nil {
		return "", fmt.Errorf("grant lookup: %w", err)
	}
	if !ok {
		return "", errors.New("no ownership and no valid grant")
	}
	return "grant", nil
}

// open opens every entry, requiring each envelope to be bound to accountID
// and to hold exactly the selected key.
func (s *Server) open(entries []*secretdpb.SealedEntry, accountID string) (map[string]string, error) {
	if len(s.identities) == 0 {
		return nil, errors.New("secretd holds no runtime identity")
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key := entry.GetKey()
		source := entry.GetSourceKey()
		if source == "" {
			source = key
		}
		if api.ValidateEnvKey(key) != nil || api.ValidateEnvKey(source) != nil {
			clear(values)
			return nil, fmt.Errorf("entry %s: invalid key", logsanitize.Field(key))
		}
		if _, dup := values[key]; dup {
			clear(values)
			return nil, fmt.Errorf("entry %s: duplicate key", logsanitize.Field(key))
		}
		inner, binding, err := secretbox.OpenMultiBound(s.identities, entry.GetCiphertext())
		if err != nil {
			clear(values)
			return nil, fmt.Errorf("entry %s: %w", logsanitize.Field(key), err)
		}
		if err := binding.Require(accountID); err != nil {
			clear(inner)
			clear(values)
			return nil, fmt.Errorf("entry %s: %w", logsanitize.Field(key), err)
		}
		value, ok := inner[source]
		if !ok || len(inner) != 1 {
			clear(inner)
			clear(values)
			return nil, fmt.Errorf("entry %s: envelope does not hold exactly the selected key", logsanitize.Field(key))
		}
		values[key] = value
		clear(inner)
	}
	return values, nil
}

// GrantPeer implements secretdpb.SecretdServer.
func (s *Server) GrantPeer(ctx context.Context, req *secretdpb.GrantPeerRequest) (*secretdpb.GrantPeerResponse, error) {
	name, nodeID, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	appID := req.GetAppId()
	if appID == "" || strings.TrimSpace(req.GetNodeName()) == "" {
		return nil, rpcErr(codes.InvalidArgument, "secretd needs an app id and a node name")
	}
	target, err := s.store.AppTarget(ctx, appID)
	if err != nil {
		return nil, rpcErr(codes.NotFound, "secretd found no such app")
	}
	if target.OwnerNodeID == "" || target.OwnerNodeID != nodeID {
		s.log.Warn("secretd: refused grant from a non-owner", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID))
		return nil, rpcErr(codes.PermissionDenied, "secretd refused: only the app's owner node may grant")
	}
	peerID, err := s.store.NodeIDByName(ctx, req.GetNodeName())
	if errors.Is(err, ErrUnknownNode) {
		return nil, rpcErr(codes.NotFound, "secretd found no active node of that name")
	}
	if err != nil {
		return nil, rpcErr(codes.Unavailable, "secretd could not look up the peer")
	}
	if peerID == nodeID {
		return nil, rpcErr(codes.InvalidArgument, "the owner needs no grant")
	}
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	ttl = min(max(ttl, MinGrantTTL), MaxGrantTTL)
	expires, err := s.store.UpsertGrant(ctx, appID, peerID, nodeID, s.now().Add(ttl))
	if err != nil {
		s.log.Error("secretd: record grant", "app_id", logsanitize.Field(appID), "err", err)
		return nil, rpcErr(codes.Unavailable, "secretd could not record the grant")
	}
	return &secretdpb.GrantPeerResponse{ExpiresAt: expires.UTC().Format(time.RFC3339)}, nil
}

// appBudget counts the distinct apps each node opened in a rolling window.
type appBudget struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	seen   map[string]map[string]time.Time // node -> app -> last open
}

func newAppBudget(limit int, window time.Duration) *appBudget {
	return &appBudget{limit: limit, window: window, seen: map[string]map[string]time.Time{}}
}

// allow records node opening app at now and reports whether node stays
// within its distinct-app budget. Re-opening an app it already holds is
// always allowed.
func (b *appBudget) allow(node, app string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	apps := b.seen[node]
	if apps == nil {
		apps = map[string]time.Time{}
		b.seen[node] = apps
	}
	for id, at := range apps {
		if now.Sub(at) >= b.window {
			delete(apps, id)
		}
	}
	if _, held := apps[app]; !held && len(apps) >= b.limit {
		return false
	}
	apps[app] = now
	return true
}
