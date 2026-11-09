//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// The broker only bounds frames and adds identity. Profile protobuf parsing,
// decompression and backend traffic happen in the unprivileged profiled daemon.
func startProfilingReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, store state.Store, jailer *fcvm.JailerVMM, reg prometheus.Registerer) error {
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
	broker := newProfileBroker(reg)
	broker.identity = func(instance string) (string, error) {
		identity, err := mgr.InstanceProfileIdentity(instance)
		return identity.Generation, err
	}
	broker.resolve = func(ctx context.Context, instance string) (profiling.Principal, error) {
		return resolveProfilePrincipal(ctx, mgr, store, instance)
	}
	broker.send = func(ctx context.Context, e profiling.Envelope) error { return profiling.Send(ctx, client, e) }
	return jailer.RegisterGuestVsockStreamHandler(api.ProfileVsockPort, func(instance string, conn net.Conn) (string, error) {
		requestCtx, cancel := context.WithTimeout(ctx, api.ProfileTransportTimeout)
		defer cancel()
		_ = conn.SetDeadline(time.Now().Add(api.ProfileTransportTimeout))
		ack, result, err := broker.handle(requestCtx, instance, conn)
		_, _ = conn.Write([]byte{ack})
		if err != nil {
			return result, err
		}
		return "", nil
	})
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
