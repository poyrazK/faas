//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// The broker only bounds frames and adds identity. Profile protobuf parsing,
// decompression and backend traffic happen in the unprivileged profiled daemon.
func startProfilingReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, store state.Store, jailer *fcvm.JailerVMM) error {
	if os.Getenv("FAAS_PROFILING_ENABLED") != "1" {
		return nil
	}
	socket := os.Getenv("FAAS_PROFILE_SOCKET")
	if socket == "" {
		socket = api.ProfileDefaultSocket
	}
	client, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(api.ProfileMaxFrameBytes+api.ProfileRPCOverheadBytes)))
	if err != nil {
		return fmt.Errorf("profile service connection: %w", err)
	}
	go func() { <-ctx.Done(); _ = client.Close() }()
	broker := newProfileBroker(mgr, store)
	return jailer.RegisterGuestVsockStreamHandler(api.ProfileVsockPort, func(instance string, conn net.Conn) (string, error) {
		requestCtx, cancel := context.WithTimeout(ctx, api.ProfileTransportTimeout)
		defer cancel()
		_ = conn.SetDeadline(time.Now().Add(api.ProfileTransportTimeout))
		ack := byte(1)
		defer func() { _, _ = conn.Write([]byte{ack}) }()
		kind, err := broker.serve(requestCtx, conn, instance, func(ctx context.Context, env profiling.Envelope) error {
			return profiling.Send(ctx, client, env)
		})
		if err == nil {
			ack = 0
		}
		return kind, err
	})
}

// profileBroker bounds untrusted profile uploads before they cost the node
// anything shared (profiling review round 4, R4-3). One stream per instance
// is in flight at a time, so a guest opening many streams holds at most one
// of the node-wide slots; the frame length is read and checked before a slot
// is taken; and the instance principal is cached briefly, so a steady upload
// stream does not repeat the app, deployment, owner and route-policy reads on
// every connection.
type profileBroker struct {
	identity func(instance string) (fcvm.ProfileInstanceIdentity, error)
	resolve  func(ctx context.Context, instance string) (profiling.Principal, error)
	slots    chan struct{}

	mu         sync.Mutex
	inFlight   map[string]bool
	principals map[string]cachedProfilePrincipal
}

type cachedProfilePrincipal struct {
	principal profiling.Principal
	expires   time.Time
}

// profilePrincipalTTL bounds how long a changed app setting (profiling turned
// off, a plan change, new declared routes) takes to reach the broker.
const (
	profilePrincipalTTL        = 30 * time.Second
	profilePrincipalCacheLimit = 4096
)

func newProfileBroker(mgr *fcvm.Manager, store state.Store) *profileBroker {
	return newProfileBrokerWith(mgr.InstanceProfileIdentity, func(ctx context.Context, instance string) (profiling.Principal, error) {
		return resolveProfilePrincipal(ctx, mgr, store, instance)
	})
}

func newProfileBrokerWith(identity func(string) (fcvm.ProfileInstanceIdentity, error), resolve func(context.Context, string) (profiling.Principal, error)) *profileBroker {
	return &profileBroker{identity: identity, resolve: resolve, slots: make(chan struct{}, api.ProfileMaxConcurrentUploads),
		inFlight: map[string]bool{}, principals: map[string]cachedProfilePrincipal{}}
}

func (b *profileBroker) serve(ctx context.Context, conn io.Reader, instance string, send func(context.Context, profiling.Envelope) error) (string, error) {
	if !b.claimInstance(instance) {
		return "overload", fmt.Errorf("profile upload already in flight for instance")
	}
	defer b.releaseInstance(instance)
	var length [4]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return "read", err
	}
	n := binary.BigEndian.Uint32(length[:])
	if n == 0 || n > api.ProfileMaxFrameBytes {
		return "protocol", fmt.Errorf("profile frame exceeds bounds")
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		return "overload", fmt.Errorf("profile broker busy")
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(conn, body); err != nil {
		return "read", err
	}
	var upload profiling.Upload
	if err := json.Unmarshal(body, &upload); err != nil {
		return "protocol", fmt.Errorf("invalid profile upload")
	}
	if len(upload.Profile) > api.ProfileMaxCompressedBytes {
		return "protocol", fmt.Errorf("profile payload exceeds bounds")
	}
	principal, err := b.principal(ctx, instance)
	if err != nil {
		return "identity", err
	}
	if err := send(ctx, profiling.Envelope{Principal: principal, Upload: upload}); err != nil {
		return "forward", err
	}
	return "accepted", nil
}

func (b *profileBroker) claimInstance(instance string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inFlight[instance] {
		return false
	}
	b.inFlight[instance] = true
	return true
}

func (b *profileBroker) releaseInstance(instance string) {
	b.mu.Lock()
	delete(b.inFlight, instance)
	b.mu.Unlock()
}

// principal reads the instance's live identity from vmmd memory on every
// upload, so a new lifetime never reuses an old one's principal; only the
// database-derived principal is cached, keyed by instance and generation.
func (b *profileBroker) principal(ctx context.Context, instance string) (profiling.Principal, error) {
	identity, err := b.identity(instance)
	if err != nil {
		return profiling.Principal{}, err
	}
	key := instance + "|" + identity.Generation
	now := time.Now()
	b.mu.Lock()
	cached, ok := b.principals[key]
	b.mu.Unlock()
	if ok && now.Before(cached.expires) {
		return cached.principal, nil
	}
	principal, err := b.resolve(ctx, instance)
	if err != nil {
		return profiling.Principal{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.principals) >= profilePrincipalCacheLimit {
		for k, v := range b.principals {
			if !now.Before(v.expires) {
				delete(b.principals, k)
			}
		}
		if len(b.principals) >= profilePrincipalCacheLimit {
			clear(b.principals)
		}
	}
	b.principals[instance+"|"+principal.Generation] = cachedProfilePrincipal{principal: principal, expires: now.Add(profilePrincipalTTL)}
	return principal, nil
}

func resolveProfilePrincipal(ctx context.Context, mgr *fcvm.Manager, store state.Store, id string) (profiling.Principal, error) {
	if store == nil {
		return profiling.Principal{}, fmt.Errorf("profile identity store unavailable")
	}
	identity, err := mgr.InstanceProfileIdentity(id)
	if err != nil {
		return profiling.Principal{}, err
	}
	app, err := store.AppByID(ctx, identity.AppID)
	if err != nil || app.AccountID != identity.AccountID || app.Manifest.Profiling == nil || !app.Manifest.Profiling.Enabled {
		return profiling.Principal{}, fmt.Errorf("profiling disabled for instance")
	}
	dep, err := store.DeploymentByID(ctx, identity.DeploymentID)
	if err != nil || dep.AppID != app.ID {
		return profiling.Principal{}, fmt.Errorf("profile deployment unavailable")
	}
	// An organization's app is judged by the organization's plan and counts
	// in its upload quota, whoever created the app (ADR-819, ADR-1155).
	owner, err := state.AppResourceAccount(ctx, store, app)
	if err != nil {
		return profiling.Principal{}, fmt.Errorf("profile account unavailable")
	}
	scope := dep.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if identity.Runtime == "" {
		identity.Runtime = app.Runtime
	}
	if identity.Runtime == "" {
		identity.Runtime = "custom"
	}
	labels := profileDeclaredRouteLabels(ctx, store, app.AccountID, app, scope)
	return profiling.Principal{Routes: labels, AccountID: app.AccountID, AppID: app.ID, DeploymentID: dep.ID, InstanceID: id, Generation: identity.Generation, StartedAt: identity.StartedAt, Scope: scope, Runtime: identity.Runtime,
		Plan: owner.Plan, Pool: owner.ResourceOwnerID()}, nil
}

func profileDeclaredRouteLabels(ctx context.Context, store state.Store, accountID string, app state.App, scope string) []string {
	routes := app.DeclaredRoutes
	if policy, err := store.GetProjectEnvironmentRoutePolicy(ctx, accountID, app.ID, scope); err == nil {
		routes = policy.DeclaredRoutes
	} else if !errors.Is(err, state.ErrNotFound) {
		routes = nil // Failed policy reads cannot authorize route attribution.
	}
	allowed := map[string]bool{}
	for _, r := range routes {
		for _, method := range r.Methods {
			label := method + " " + r.Path
			if api.ValidProfileRoute(label) {
				allowed[label] = true
			}
		}
	}
	labels := make([]string, 0, len(allowed))
	for label := range allowed {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	if len(labels) > api.ProfileRouteMaxLabels {
		labels = labels[:api.ProfileRouteMaxLabels]
	}
	return labels
}
