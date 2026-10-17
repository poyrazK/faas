//go:build metal && function_background_native

package e2e_test

// adr: 1547

import (
	"context"
	"maps"
	"net/http"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
)

func verifyNativeMigrationBuildInputs(t *testing.T, ctx context.Context, f *nativeDestinationFixture, ins state.Instance, receipt runtimeadmission.Receipt) {
	t.Helper()
	app, err := f.store.AppByID(ctx, f.appID)
	if err != nil {
		t.Fatal("read native fixture app")
	}
	producer, err := f.store.GetCurrentSourceBuildRootfs(ctx, app.AccountID, app.ID, ins.DeploymentID)
	if err != nil || producer.Input.Kind != "function-layer" || producer.Input.Runtime != f.run.Runtime ||
		producer.Input.GuestInitDigest != "sha256:"+f.config.Builds["guest_init"] || producer.Input.RunnerDigest != "sha256:"+f.config.Builds[f.run.Runtime] {
		t.Fatal("consumed function layer does not bind the freshly built guest and runner")
	}
	base, err := f.store.GetBaseImageProducerByID(ctx, producer.Input.BaseProducerID)
	if err != nil || base.InputHash != producer.Input.BaseInputHash || base.Input.GuestInitDigest != producer.Input.GuestInitDigest {
		t.Fatal("consumed base does not bind the freshly built guest-init")
	}
	mainFound, baseFound := false, false
	for _, drive := range receipt.ArtifactConsumption.Drives {
		if drive.Source.Role() == "main" {
			mainFound = drive.Source.StorageKey == producer.Input.StorageKey && drive.Source.Digest == producer.Input.ArtifactDigest && drive.Source.Bytes == producer.Input.ArtifactBytes
		}
		if drive.Source.Role() == "base" {
			baseFound = drive.Source.StorageKey == base.Input.Artifact.StorageKey && drive.Source.Digest == base.Input.Artifact.Digest && drive.Source.Bytes == base.Input.Artifact.Bytes
		}
	}
	if !mainFound || !baseFound {
		t.Fatal("native process did not consume the exact fresh producer artifacts")
	}
	f.run.RootfsInputSHA, f.run.BaseInputSHA = producer.InputHash, base.InputHash
}

func seedNativeMigrationPools(t *testing.T, ctx context.Context, f *nativeDestinationFixture, source *sched.VMMClient, id string) {
	t.Helper()
	lock := f.sql.Lock(ctx, t)
	workers := map[int]bool{}
	for _, row := range []string{"one", "two", "three"} {
		reply := f.get(t, ctx, "tail/"+row, http.StatusAccepted)
		workers[reply.PID] = true
	}
	backgroundNativeWait(t, ctx, func() bool { _, blocked := f.sql.Connections(ctx, t); return blocked == 3 })
	workers[f.get(t, ctx, "state", http.StatusOK).PID] = true
	if len(workers) != 4 {
		t.Fatal("native migration did not initialize four distinct interpreters")
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal("release native SQL witness lock")
	}
	backgroundNativeWait(t, ctx, func() bool {
		all, _ := f.sql.Connections(ctx, t)
		return all == 8 && nativeSQLIdle(ctx, source, id) && len(f.sql.Rows(ctx, t)) == 3
	})
	f.run.SourceSQL, f.run.RowsBefore = f.sql.Sessions(ctx, t), f.sql.Rows(ctx, t)
}

func verifyNativeReadyDestination(t *testing.T, ctx context.Context, f *nativeDestinationFixture, source state.Instance, destination state.FunctionMigrationDestination, conn *grpc.ClientConn) {
	t.Helper()
	r := f.run
	owner, err := f.store.InstanceByID(ctx, source.ID)
	if err != nil || destination.Phase != state.FunctionDestinationReady || destination.Receipt == nil ||
		owner.State != string(state.StateMigrating) || owner.NodeID != source.NodeID || owner.WakeID != source.WakeID ||
		destination.Grant.Binding.NodeID != r.DestinationNode || destination.Grant.CheckReceipt(*destination.Receipt, time.Now()) != nil {
		t.Fatal("readiness published ownership or omitted exact measured consumption")
	}
	r.ReadyKeptSource, r.DestinationWake = true, destination.Grant.WakeID
	r.Destination = nativeReceiptOwner(t, *destination.Receipt)
	nativeMigrationProbe(t, ctx, r.DestinationNode, "owner", &r.Destination)
	r.Destination.Verified = true
	if !nativeDestinationExecutionRefused(ctx, conn, source.ID, f.slug+"."+f.config.AppDomain) {
		t.Fatal("ready destination did not explicitly refuse customer execution before commit")
	}
	r.ExecutionRefused = true
	if !maps.Equal(r.RowsBefore, f.sql.Rows(ctx, t)) {
		t.Fatal("pre-commit execution changed committed SQL")
	}
}

func nativeDestinationExecutionRefused(ctx context.Context, conn *grpc.ClientConn, instance, host string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	stream, err := vmmdpb.NewVmmdClient(conn).ForwardHTTPStream(ctx)
	if err != nil {
		return false
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{
		Instance: instance, Method: "GET", RequestUri: "/tail/before-commit", ContentLengthKnown: true,
		Headers: []*vmmdpb.Header{{Name: "Host", Value: host}}, AppProtocol: "http1",
	}}}); err != nil {
		return false
	}
	if err := stream.CloseSend(); err != nil {
		return false
	}
	reply, err := stream.Recv()
	if err != nil {
		p, ok := grpcerr.FromStatus(err)
		return ok && p.Code == api.CodeInstanceBusy
	}
	return reply.GetInit().GetStatus() == http.StatusServiceUnavailable
}

func verifyNativeCommittedDestination(t *testing.T, ctx context.Context, f *nativeDestinationFixture, source state.Instance, destination state.FunctionMigrationDestination, sourceClient *sched.VMMClient) {
	t.Helper()
	r := f.run
	owner, err := f.store.InstanceByID(ctx, source.ID)
	if err != nil || owner.State != string(state.StateRunning) || owner.NodeID != r.DestinationNode || owner.WakeID != r.DestinationWake ||
		owner.Netns != destination.Receipt.Netns || owner.HostIP != destination.Receipt.HostIP || owner.GuestUID != int(destination.Receipt.LeaseUID) {
		t.Fatal("committed instance does not match the measured destination")
	}
	if err := f.store.CheckFunctionMigrationDestinationCommitted(ctx, destination.Grant); err != nil {
		t.Fatal("destination has no current serving authority", err)
	}
	receipt, err := f.store.GetInstanceApplicationStandardRuntimeReceipt(ctx, source.ID)
	if err != nil || !receipt.Equal(*destination.Receipt) {
		t.Fatal("ownership did not publish the exact destination native epoch")
	}
	inputs, found, err := f.store.InstanceRuntimeConfigReceipt(ctx, source.ID)
	if err != nil || !found || inputs.Scope != state.DefaultEnvScope {
		t.Fatal("ownership did not retain its runtime configuration receipt")
	}
	if fresh, err := f.store.RuntimeConfigInputsFresh(ctx, f.appID, inputs); err != nil || !fresh {
		t.Fatal("committed runtime configuration is stale")
	}
	r.CommitVerified = true
	f.get(t, ctx, "state", http.StatusOK) // Real gateway routes to the new owner.
	r.RowsAfter = f.sql.Rows(ctx, t)
	backgroundNativeWait(t, ctx, func() bool {
		return nativeSourceRetired(ctx, f.store, sourceClient, source, destination.Grant.Source.LeaseToken)
	})
	nativeMigrationProbe(t, ctx, source.NodeID, "retired", &r.Source)
	r.SourceRetired, r.SourceLeaseRemoved = true, true
}
