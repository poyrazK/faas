//go:build metal && function_background_native

package e2e_test

// adr: 1547
// Controlled reply discards exercise native effects at the client boundary;
// they do not qualify real packet loss, PostgreSQL failover or vsock faults.

import (
	"context"
	"errors"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/e2etest/functionmigration"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type nativeAdoptionCall struct {
	request *vmmdpb.AdoptMigratedInstanceRequest
	first   *vmmdpb.AdoptMigratedInstanceResponse
	reply   *vmmdpb.AdoptMigratedInstanceResponse
	invoke  func(context.Context, *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error)
}

func captureNativeAdoption(ctx context.Context, method string, request, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, options []grpc.CallOption) (*nativeAdoptionCall, error) {
	req, ok := request.(*vmmdpb.AdoptMigratedInstanceRequest)
	response, replyOK := reply.(*vmmdpb.AdoptMigratedInstanceResponse)
	if !ok || !replyOK {
		return nil, errors.New("native adoption observer received an unrelated RPC")
	}
	outgoing, _ := metadata.FromOutgoingContext(ctx)
	call := &nativeAdoptionCall{request: proto.Clone(req).(*vmmdpb.AdoptMigratedInstanceRequest),
		first: proto.Clone(response).(*vmmdpb.AdoptMigratedInstanceResponse), reply: response}
	call.invoke = func(retryCtx context.Context, duplicate *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
		result := new(vmmdpb.AdoptMigratedInstanceResponse)
		retryCtx = metadata.NewOutgoingContext(retryCtx, outgoing.Copy())
		// Use the actual downstream invoker, bypassing only this observation
		// barrier. Every duplicate still reaches the remote vmmd's real RPC.
		err := invoke(retryCtx, method, duplicate, result, conn, options...)
		return result, err
	}
	return call, nil
}

func (c *nativeAdoptionCall) discard() error {
	proto.Reset(c.reply) // The scheduler has not received this reply yet.
	return functionmigration.ErrReplyDiscarded
}

func exerciseNativeAdoptionReplies(t *testing.T, ctx context.Context, f *nativeDestinationFixture, call *nativeAdoptionCall, source state.Instance, destination state.FunctionMigrationDestination, conn *grpc.ClientConn) {
	t.Helper()
	if call == nil {
		t.Fatal("native readiness observer omitted its completed RPC")
	}
	fault := &functionmigration.ReplyFault{Mode: "explicit-concurrent-retry"}
	copies := 3
	if f.run.Scenario == functionmigration.LostReadiness {
		if !errors.Is(call.discard(), functionmigration.ErrReplyDiscarded) {
			t.Fatal("native readiness reply was not discarded")
		}
		fault.Mode, fault.ReadinessDiscards, copies = "client-reply-discard", 1, 1
	}
	var recovered *vmmdpb.AdoptMigratedInstanceResponse
	replayed, err := functionmigration.ReplayAdoption(ctx, call.request, call.first,
		func(ctx context.Context, request *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
			reply, err := call.invoke(ctx, request)
			if copies == 1 {
				recovered = reply // One retry; joined before the observer reads it.
			}
			return reply, err
		}, copies)
	if err != nil {
		t.Fatal("identical native adoption retry failed")
	}
	fault.Replays = &replayed
	if copies == 1 {
		// Forward the actual retry's reply, never a manufactured success.
		proto.Merge(call.reply, recovered)
	}
	verifyNativeAdoptionReplay(t, ctx, f, fault, source, destination, conn)
	f.run.Fault = fault
}

func verifyNativeAdoptionReplay(t *testing.T, ctx context.Context, f *nativeDestinationFixture, fault *functionmigration.ReplyFault, source state.Instance, before state.FunctionMigrationDestination, conn *grpc.ClientConn) {
	t.Helper()
	after, err := f.store.GetFunctionMigrationDestination(ctx, source.ID, before.Grant.Source.LeaseToken)
	if err != nil || after.Phase != state.FunctionDestinationReady || after.Receipt == nil || !after.Grant.Equal(before.Grant) || !after.Receipt.Equal(*before.Receipt) {
		t.Fatal("native duplicate adoption replaced the ready destination epoch")
	}
	fault.OwnerAfterReplays = nativeReceiptOwner(t, *after.Receipt)
	nativeMigrationProbe(t, ctx, f.run.DestinationNode, "owner", &fault.OwnerAfterReplays)
	fault.OwnerAfterReplays.Verified = true
	fault.SQLAfterReplays = f.sql.Sessions(ctx, t)
	if !functionmigration.SameSessions(f.run.DestinationSQL, fault.SQLAfterReplays) {
		t.Fatal("native adoption retry closed or replaced recovered SQL sessions")
	}
	owner, err := f.store.InstanceByID(ctx, source.ID)
	if err != nil || owner.NodeID != source.NodeID || owner.WakeID != source.WakeID || owner.State != string(state.StateMigrating) {
		t.Fatal("native adoption retry published ownership before commit")
	}
	fault.SourceKeptAfterReplay = true
	if !nativeDestinationExecutionRefused(ctx, conn, source.ID, f.slug+"."+f.config.AppDomain) {
		t.Fatal("native adoption retry opened execution before ownership committed")
	}
	fault.ClosedAfterReplay = true
}
