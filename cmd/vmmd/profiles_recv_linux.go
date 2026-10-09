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
	slots := make(chan struct{}, api.ProfileMaxConcurrentUploads)
	return jailer.RegisterGuestVsockStreamHandler(api.ProfileVsockPort, func(instance string, conn net.Conn) (string, error) {
		requestCtx, cancel := context.WithTimeout(ctx, api.ProfileTransportTimeout)
		defer cancel()
		_ = conn.SetDeadline(time.Now().Add(api.ProfileTransportTimeout))
		ack := byte(1)
		defer func() { _, _ = conn.Write([]byte{ack}) }()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			return "limited", fmt.Errorf("profile broker busy")
		}
		var length [4]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return "read", err
		}
		n := binary.BigEndian.Uint32(length[:])
		if n == 0 || n > api.ProfileMaxFrameBytes {
			return "protocol", fmt.Errorf("profile frame exceeds bounds")
		}
		body := make([]byte, int(n))
		if _, err := io.ReadFull(conn, body); err != nil {
			return "read", err
		}
		principal, err := resolveProfilePrincipal(requestCtx, mgr, store, instance)
		if err != nil {
			return "identity", err
		}
		var upload profiling.Upload
		if err := json.Unmarshal(body, &upload); err != nil {
			return "protocol", fmt.Errorf("invalid profile upload")
		}
		if len(upload.Profile) > api.ProfileMaxCompressedBytes {
			return "protocol", fmt.Errorf("profile payload exceeds bounds")
		}
		if err := profiling.Send(requestCtx, client, profiling.Envelope{Principal: principal, Upload: upload}); err != nil {
			return "forward", err
		}
		ack = 0
		return "accepted", nil
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
	acct, err := store.AccountByID(ctx, identity.AccountID)
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
	labels := profileDeclaredRouteLabels(ctx, store, acct.ID, app, scope)
	return profiling.Principal{Routes: labels, AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, InstanceID: id, Generation: identity.Generation, StartedAt: identity.StartedAt, Scope: scope, Runtime: identity.Runtime, Plan: acct.Plan}, nil
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
