// adr: 1027
package main

import (
	"context"
	"strings"
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
	v := databaseNodeVerifier(admitPeers{"db-2.faas": true})
	for _, cn := range []string{"schedd.faas", "pgproxyd.faas", "db-2.faas"} {
		if err := v.LookupCN(cn); err != nil {
			t.Errorf("%s refused: %v", cn, err)
		}
	}
	for _, cn := range []string{"gatewayd.faas", "builderd.faas", "imaged.faas", "fsn-2.faas", "db-3.faas"} {
		if err := v.LookupCN(cn); err == nil {
			t.Errorf("%s admitted to a database node", cn)
		}
	}
	if err := databaseNodeVerifier(nil).LookupCN("db-2.faas"); err == nil {
		t.Error("a node without peers admitted a database node")
	}
}

// admitPeers stands in for the vmmd server's current standby nodes.
type admitPeers map[string]bool

func (a admitPeers) AdmitsDatabasePeer(cn string) bool { return a[cn] }

func TestRunDatabaseNodeRequiresDatabaseVMs(t *testing.T) {
	if err := runDatabaseNode(context.Background(), &Config{}, nil, nil, runDeps{}); err == nil {
		t.Fatal("a database node without [database_vms] started")
	}
}

// Every database RPC the service declares is served on a database node:
// one left off the list fails there with Unimplemented, which tests that
// call the handlers directly never see.
func TestDatabaseNodeServesEveryDatabaseRPC(t *testing.T) {
	desc := vmmdpb.Vmmd_ServiceDesc
	names := make([]string, 0, len(desc.Methods)+len(desc.Streams))
	for _, m := range desc.Methods {
		names = append(names, m.MethodName)
	}
	for _, s := range desc.Streams {
		names = append(names, s.StreamName)
	}
	served := 0
	for _, name := range names {
		if !strings.Contains(name, "Database") {
			continue
		}
		full := "/" + desc.ServiceName + "/" + name
		if !databaseMethods[full] {
			t.Errorf("%s is a database RPC that database nodes refuse", full)
		}
		served++
	}
	if served != len(databaseMethods) {
		t.Errorf("%d database RPCs declared, %d allowed", served, len(databaseMethods))
	}
}
