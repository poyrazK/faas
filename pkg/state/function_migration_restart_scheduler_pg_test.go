//go:build !no_pg

package state_test

// adr: 1548
// Real PostgreSQL and scheduler startup with a simulated VM RPC peer. This is
// composition/failure validation, not native process or SQL pool acceptance.

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type restartCatalogPeer struct {
	vmmdpb.UnimplementedVmmdServer
	store             *state.PgStore
	d                 state.FunctionMigrationDestination
	activations, acks atomic.Int32
}

func (p *restartCatalogPeer) AdoptMigratedInstance(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	g := p.d.Grant
	if req.GetInstanceId() != g.Binding.InstanceID || req.GetLeaseToken() != g.Source.LeaseToken || req.GetWakeId() != g.WakeID || p.store.CheckFunctionMigrationDestinationCommitted(ctx, g) != nil {
		return nil, status.Error(codes.FailedPrecondition, "activation requires the exact committed epoch")
	}
	p.activations.Add(1)
	r := p.d.Receipt
	return &vmmdpb.AdoptMigratedInstanceResponse{WakeId: g.WakeID, Method: vmmdpb.WakeMethod_WAKE_RESTORE, Netns: r.Netns, HostIp: r.HostIP, GuestUid: int32(r.LeaseUID)}, nil
}
func (p *restartCatalogPeer) AcknowledgeMigration(ctx context.Context, req *vmmdpb.AcknowledgeMigrationRequest) (*vmmdpb.AcknowledgeMigrationResponse, error) {
	g := p.d.Grant
	if req.GetInstanceId() != g.Binding.InstanceID || req.GetLeaseToken() != g.Source.LeaseToken || p.activations.Load() != 1 || p.store.CheckFunctionMigrationDestinationCommitted(ctx, g) != nil {
		return nil, status.Error(codes.FailedPrecondition, "source retirement preceded activation")
	}
	if err := p.store.DeleteMigrationLease(ctx, g.Source.LeaseToken); err != nil {
		return nil, err
	}
	p.acks.Add(1)
	return &vmmdpb.AcknowledgeMigrationResponse{}, nil
}
func restartCatalogRouter(t *testing.T, p *restartCatalogPeer) *sched.VMMRouter {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, p)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := wire.DialContext(t.Context(), "unix:///restart-catalog", nil, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	router := sched.NewVMMRouter(nil, nil, nil)
	client := sched.NewVMMClient(conn)
	router.SetClient(p.d.Grant.Binding.NodeID, client)
	router.SetClient(p.d.Grant.Source.Parent.Binding.NodeID, client)
	return router
}

type restartCatalogCommitError struct{ PgStore *state.PgStore }

func (s *restartCatalogCommitError) MigrateInstanceOwnerWithRuntimeConfig(ctx context.Context, id, from, to, token string, input state.RuntimeConfigMigration) error {
	if err := s.PgStore.MigrateInstanceOwnerWithRuntimeConfig(ctx, id, from, to, token, input); err != nil {
		return err
	}
	return errors.New("controlled error after real startup commit")
}

func TestFunctionMigrationRestartSchedulerPg(t *testing.T) {
	for _, scenario := range []string{"ready", "committed", "startup commit reply lost", "changed configuration", "changed target incarnation"} {
		t.Run(scenario, func(t *testing.T) {
			pool, source, d := state.FunctionMigrationRestartFixtureForTest(t)
			fresh, err := pgxpool.NewWithConfig(t.Context(), pool.Config())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(fresh.Close)
			s := state.NewPgStore(fresh)
			g := d.Grant
			var backing state.Store = s
			if scenario == "committed" {
				inputs, found, err := s.InstanceRuntimeConfigReceipt(t.Context(), source.ID)
				if err != nil || !found {
					t.Fatal("source capture lacks persisted configuration", err)
				}
				if err := s.MigrateInstanceOwnerWithRuntimeConfig(t.Context(), source.ID, source.NodeID, g.Binding.NodeID, g.Source.LeaseToken, state.RuntimeConfigMigration{ExpectedWakeID: source.WakeID, WakeID: g.WakeID, Inputs: &inputs, Netns: d.Receipt.Netns, HostIP: d.Receipt.HostIP, GuestUID: int(d.Receipt.LeaseUID)}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "startup commit reply lost" {
				backing = statetest.NewPg(&restartCatalogCommitError{PgStore: s})
			}
			if scenario == "changed configuration" {
				app, err := s.AppByID(t.Context(), source.AppID)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.UpsertAppEnvInScope(t.Context(), app.AccountID, app.ID, state.DefaultEnvScope, "AFTER_CAPTURE", "changed"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "changed target incarnation" {
				if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ArtifactProtocolVersion, NodeID: g.Binding.NodeID, Incarnation: uuid.NewString()}); err != nil {
					t.Fatal(err)
				}
			}
			peer := &restartCatalogPeer{store: s, d: d}
			engine, err := sched.NewEngine(t.Context(), backing, sched.NewLedger(), restartCatalogRouter(t, peer), nil, g.Source.FCVersion, nil)
			if err != nil {
				t.Fatal(err)
			}
			engine.WithOwnerNodeID(g.Binding.NodeID)
			n, err := engine.RecoverFunctionMigrations(t.Context())
			failed := scenario == "changed configuration" || scenario == "changed target incarnation"
			if failed {
				if err == nil || n != 0 || peer.activations.Load() != 0 || peer.acks.Load() != 0 {
					t.Fatal("stale attempt performed physical work", n, err)
				}
				owner, readErr := s.InstanceByID(t.Context(), source.ID)
				if readErr != nil || owner.NodeID != source.NodeID || owner.WakeID != source.WakeID || owner.State != string(state.StateMigrating) {
					t.Fatal("stale startup moved source ownership", readErr)
				}
				if _, err := s.GetMigrationLease(t.Context(), source.ID, g.Source.LeaseToken); err != nil {
					t.Fatal("failed startup dropped cleanup authority", err)
				}
				return
			}
			if err != nil || n != 1 || peer.activations.Load() != 1 || peer.acks.Load() != 1 {
				t.Fatal("startup did not reconstruct and finish the exact epoch", n, err)
			}
			owner, err := s.InstanceByID(t.Context(), source.ID)
			if err != nil || owner.NodeID != g.Binding.NodeID || owner.WakeID != g.WakeID || owner.State != string(state.StateRunning) {
				t.Fatal("startup published another owner", err)
			}
			if err := s.CheckFunctionMigrationDestinationCommitted(t.Context(), g); err != nil {
				t.Fatal("source cleanup removed serving authority", err)
			}
			if err := engine.SeedLedger(t.Context()); err != nil {
				t.Fatal(err)
			}
			if n, err := engine.RecoverFunctionMigrations(t.Context()); err != nil || n != 0 || peer.activations.Load() != 1 {
				t.Fatal("completed startup replayed physical work", n, err)
			}
		})
	}
}
