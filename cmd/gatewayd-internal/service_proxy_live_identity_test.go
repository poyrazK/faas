package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type liveCallerFixture struct {
	task          state.AppTask
	live          *vmmdpb.ResolveServiceCallerResponse
	rows          []state.Instance
	err           error
	ordinaryReads int
}

func (f *liveCallerFixture) ResolveServiceCaller(_ context.Context, _ *vmmdpb.ResolveServiceCallerRequest, _ ...grpc.CallOption) (*vmmdpb.ResolveServiceCallerResponse, error) {
	return f.live, f.err
}

func (f *liveCallerFixture) AppTaskByID(context.Context, string, string, string) (state.AppTask, error) {
	return f.task, nil
}

func (f *liveCallerFixture) LiveInstancesByHostIP(context.Context, string, string) ([]state.Instance, error) {
	f.ordinaryReads++
	return f.rows, nil
}

func managedLiveCallerFixture() *liveCallerFixture {
	const caller = "00000000-0000-4000-8000-000000000001"
	spec := api.ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: caller, Scope: "acceptance", Service: "billing", Binding: "BILLING_ENDPOINT",
		TargetAppID: "00000000-0000-4000-8000-000000000002", TargetDeploymentID: "00000000-0000-4000-8000-000000000003",
		ReleaseID: "00000000-0000-4000-8000-000000000004", ContextRevision: strings.Repeat("a", 64)}
	expires, lease := time.Now().Add(time.Minute), "lease"
	return &liveCallerFixture{
		live: &vmmdpb.ResolveServiceCallerResponse{Matched: true, InstanceId: "task", AccountId: "account", AppId: "caller", DeploymentId: caller, AppTask: true},
		task: state.AppTask{ID: "task", AccountID: "account", AppID: "caller", DeploymentID: caller, Status: state.AppTaskRunning,
			DeploymentScope: spec.Scope, LeaseToken: &lease, LeaseExpiresAt: &expires,
			BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service, Revision: spec.ContextRevision, ManagedService: &spec}},
		rows: []state.Instance{{AppID: "stale-tenant", DeploymentID: "stale-deployment"}},
	}
}

func TestManagedServiceCallerRequiresLiveTaskAndLease(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*liveCallerFixture)
		want bool
	}{
		{"exact", func(*liveCallerFixture) {}, true},
		{"retired", func(f *liveCallerFixture) { f.live.Matched = false }, false},
		{"reused", func(f *liveCallerFixture) { f.live.InstanceId = "replacement" }, false},
		{"wrong account", func(f *liveCallerFixture) { f.live.AccountId = "other" }, false},
		{"wrong caller", func(f *liveCallerFixture) { f.live.AppId = "other" }, false},
		{"wrong deployment", func(f *liveCallerFixture) { f.live.DeploymentId = "other" }, false},
		{"restoring", func(f *liveCallerFixture) { f.task.Status = state.AppTaskRestoring }, false},
		{"completed", func(f *liveCallerFixture) { f.task.Status = state.AppTaskSucceeded }, false},
		{"cancelled", func(f *liveCallerFixture) { now := time.Now(); f.task.CancelRequested = &now }, false},
		{"expired lease", func(f *liveCallerFixture) { expired := time.Now().Add(-time.Second); f.task.LeaseExpiresAt = &expired }, false},
		{"missing lease", func(f *liveCallerFixture) { f.task.LeaseToken = nil }, false},
		{"ordinary probe", func(f *liveCallerFixture) { f.task.BindingVerification.ManagedService = nil }, false},
		{"wrong context", func(f *liveCallerFixture) { f.task.BindingVerification.ManagedService.CallerDeploymentID = "other" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := managedLiveCallerFixture()
			test.edit(f)
			rows, err := serviceProxyLiveIdentity(t.Context(), f, f, "node", "10.100.0.7")
			if err != nil || (len(rows) == 1) != test.want || f.ordinaryReads != 0 {
				t.Fatalf("identity = %+v, %v; ordinary reads = %d", rows, err, f.ordinaryReads)
			}
			if test.want && (rows[0].AppID != "caller" || rows[0].DeploymentID != f.task.DeploymentID || rows[0].HostIP != "10.100.0.7") {
				t.Fatalf("wrong task identity: %+v", rows[0])
			}
		})
	}
}

func TestServiceCallerLiveLookupRollingUpgradeAndReusedAddress(t *testing.T) {
	f := managedLiveCallerFixture()
	f.err = status.Error(codes.Unimplemented, "old node")
	rows, err := serviceProxyLiveIdentity(t.Context(), f, f, "node", "10.100.0.7")
	if err != nil || len(rows) != 1 || rows[0].AppID != "stale-tenant" {
		t.Fatalf("ordinary rolling-upgrade compatibility = %+v, %v", rows, err)
	}
	f.err = errors.New("node unavailable")
	if _, err := serviceProxyLiveIdentity(t.Context(), f, f, "node", "10.100.0.7"); err == nil {
		t.Fatal("real node error fell back to database")
	}
	f.err, f.live.AppTask = nil, false
	rows, err = serviceProxyLiveIdentity(t.Context(), f, f, "node", "10.100.0.7")
	if err != nil || len(rows) != 0 {
		t.Fatalf("stale ordinary identity accepted for reused address: %+v, %v", rows, err)
	}
	f.rows = []state.Instance{{AppID: f.live.AppId, DeploymentID: f.live.DeploymentId}}
	rows, err = serviceProxyLiveIdentity(t.Context(), f, f, "node", "10.100.0.7")
	if err != nil || len(rows) != 1 {
		t.Fatalf("matching ordinary caller rejected: %+v, %v", rows, err)
	}
	f.live.AppId, f.live.DeploymentId = "", ""
	rows, err = serviceProxyLiveIdentity(t.Context(), f, f, "node", "10.100.0.7")
	if err != nil || len(rows) != 0 {
		t.Fatalf("unattributed slot inherited old identity: %+v, %v", rows, err)
	}
	f.live.JobTask = true
	rows, err = serviceProxyLiveIdentity(t.Context(), f, f, "node", "10.100.0.7")
	if err != nil || len(rows) != 1 {
		t.Fatalf("managed Job lookup was not preserved: %+v, %v", rows, err)
	}
}

type liveCallerClient struct {
	vmmdpb.VmmdClient
	fixture *liveCallerFixture
}

func (c liveCallerClient) ResolveServiceCaller(ctx context.Context, request *vmmdpb.ResolveServiceCallerRequest, opts ...grpc.CallOption) (*vmmdpb.ResolveServiceCallerResponse, error) {
	return c.fixture.ResolveServiceCaller(ctx, request, opts...)
}

type liveCallerNodes struct {
	fixture *liveCallerFixture
	asked   []string
}

func (c *liveCallerNodes) ClientFor(_ context.Context, id string) (vmmdpb.VmmdClient, io.Closer, bool) {
	c.asked = append(c.asked, id)
	return liveCallerClient{fixture: c.fixture}, nopCloser{}, true
}

func TestServiceCallerLiveLookupUsesLocalNodeIDWithoutCachingAddresses(t *testing.T) {
	f := managedLiveCallerFixture()
	nodes := &namedNodes{ids: map[string]string{"compute-a": "node-uuid"}}
	clients := &liveCallerNodes{fixture: f}
	lookup := newServiceProxyLiveLookup(f, clients, newLocalNodeID(nodes, "compute-a"))
	rows, err := lookup(t.Context(), "compute-a", "10.100.0.7")
	if err != nil || len(rows) != 1 || rows[0].AppID != "caller" {
		t.Fatalf("initial live lookup = %+v, %v", rows, err)
	}
	f.live.Matched = false
	rows, err = lookup(t.Context(), "compute-a", "10.100.0.7")
	if err != nil || len(rows) != 0 || len(clients.asked) != 2 {
		t.Fatalf("retired address was cached: %+v, %v", rows, err)
	}
	for _, asked := range clients.asked {
		if asked != "node-uuid" {
			t.Fatalf("vmmd dial used %q instead of node UUID", asked)
		}
	}
}
