// adr: 1027
package main

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// A database node must never serve the compute RPCs: nothing there can
// run an app VM, and a stray Wake must fail loudly, not reach a nil manager.
func TestDatabaseNodeServesOnlyDatabaseRPCs(t *testing.T) {
	called := false
	unary := func(context.Context, any) (any, error) { called = true; return nil, nil }
	for _, method := range []string{vmmdpb.Vmmd_CreateColdBoot_FullMethodName, vmmdpb.Vmmd_CreateFromSnapshot_FullMethodName, "/grpc.health.v1.Health/Check"} {
		_, err := databaseOnlyUnary(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: method}, unary)
		if status.Code(err) != codes.Unimplemented || called {
			t.Fatalf("%s: err = %v, handler called = %v", method, err, called)
		}
	}
	if _, err := databaseOnlyUnary(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: vmmdpb.Vmmd_StartDatabase_FullMethodName}, unary); err != nil || !called {
		t.Fatalf("StartDatabase: err = %v, handler called = %v", err, called)
	}
	stream := func(any, grpc.ServerStream) error { return nil }
	if err := databaseOnlyStream(nil, nil, &grpc.StreamServerInfo{FullMethod: vmmdpb.Vmmd_DatabaseSession_FullMethodName}, stream); err != nil {
		t.Fatalf("DatabaseSession refused: %v", err)
	}
	if err := databaseOnlyStream(nil, nil, &grpc.StreamServerInfo{FullMethod: vmmdpb.Vmmd_ForwardTCPStream_FullMethodName}, stream); status.Code(err) != codes.Unimplemented {
		t.Fatalf("ForwardTCPStream = %v, want Unimplemented", err)
	}
}

func TestDatabaseNodeAdmitsOnlySchedulerAndProxy(t *testing.T) {
	v := databaseNodeVerifier()
	for _, cn := range []string{"schedd.faas", "pgproxyd.faas"} {
		if err := v.LookupCN(cn); err != nil {
			t.Errorf("%s refused: %v", cn, err)
		}
	}
	for _, cn := range []string{"gatewayd.faas", "builderd.faas", "imaged.faas", "fsn-2.faas"} {
		if err := v.LookupCN(cn); err == nil {
			t.Errorf("%s admitted to a database node", cn)
		}
	}
}

func TestRunDatabaseNodeRequiresDatabaseVMs(t *testing.T) {
	if err := runDatabaseNode(context.Background(), &Config{}, nil, nil, runDeps{}); err == nil {
		t.Fatal("a database node without [database_vms] started")
	}
}
