// adr: 1027 — production class: a standby's node streams replication from
// its primary's node only after schedd named it that database's standby.
package vmmdgrpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/wire"
)

const replicaDB = "7d1e2f3a-4b5c-4d6e-8f9a-0b1c2d3e4f5a"

func TestReplicationControlRecordsTheStandbyNode(t *testing.T) {
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateReady,
		Replication: &dbguest.ReplicationStatus{Mode: dbguest.ReplicationSync, Streaming: true, Synchronous: true}}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	ctx := unixCaller()
	bad := map[string]*vmmdpb.DatabaseControlRequest{
		"node on another op":   {DatabaseId: replicaDB, Op: "status", StandbyNode: "db-2"},
		"async without a node": {DatabaseId: replicaDB, Op: "replication", Replication: "async"},
		"node with off":        {DatabaseId: replicaDB, Op: "replication", Replication: "off", StandbyNode: "db-2"},
		"invalid node":         {DatabaseId: replicaDB, Op: "replication", Replication: "sync", StandbyNode: "DB_2"},
	}
	for name, req := range bad {
		if _, err := s.DatabaseControl(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s = %v, want InvalidArgument", name, err)
		}
	}
	if s.AdmitsDatabasePeer("db-2.faas") {
		t.Fatal("a refused request attached a standby")
	}
	resp, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "replication", Replication: "sync", StandbyNode: "db-2"})
	if err != nil || !resp.GetReplication().GetSynchronous() || resp.GetReplication().GetMode() != "sync" {
		t.Fatalf("sync = %+v, %v", resp, err)
	}
	if last := rt.controls[len(rt.controls)-1]; last.Op != dbguest.OpReplication || last.Replication != dbguest.ReplicationSync {
		t.Fatalf("guest got %+v", last)
	}
	if !s.AdmitsDatabasePeer("db-2.faas") || s.AdmitsDatabasePeer("db-3.faas") {
		t.Fatal("only the named standby's node is admitted")
	}
	if rt.standbyNodes[replicaDB] != "db-2.faas" {
		t.Fatalf("the VM's record holds standby %q, want db-2.faas", rt.standbyNodes[replicaDB])
	}
	if _, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "replication", Replication: "off"}); err != nil {
		t.Fatal(err)
	}
	if s.AdmitsDatabasePeer("db-2.faas") {
		t.Fatal("off kept the standby admitted")
	}
	if _, kept := rt.standbyNodes[replicaDB]; kept {
		t.Fatal("off kept the standby in the VM's record")
	}
	if _, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "replication", Replication: "async", StandbyNode: "db-2.faas"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StopDatabase(ctx, &vmmdpb.StopDatabaseRequest{DatabaseId: replicaDB}); err != nil {
		t.Fatal(err)
	}
	if s.AdmitsDatabasePeer("db-2.faas") {
		t.Fatal("a stopped primary kept its standby admitted")
	}
}

// A primary that outlived the last vmmd still admits the standby its
// record names, before any replication op arrives.
func TestRestartedVmmdAdmitsRecordedStandbys(t *testing.T) {
	rt := &fakeDatabaseRuntime{standbyNodes: map[string]string{replicaDB: "db-4.faas", "4e5f6a7b-8c9d-4e0f-9a1b-2c3d4e5f6a7b": "NOT A CN"}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	if !s.AdmitsDatabasePeer("db-4.faas") {
		t.Fatal("the recorded standby is not admitted after a restart")
	}
	if s.AdmitsDatabasePeer("NOT A CN") {
		t.Fatal("an invalid recorded node was admitted")
	}
}

// replicationStream is the primary node's side of a DatabaseReplication
// call, as a given caller.
type replicationStream struct {
	grpc.ServerStream
	ctx   context.Context
	first *vmmdpb.DatabaseSessionFrame
}

func (r *replicationStream) Context() context.Context { return r.ctx }
func (r *replicationStream) Send(*vmmdpb.DatabaseSessionFrame) error {
	return nil
}

func (r *replicationStream) Recv() (*vmmdpb.DatabaseSessionFrame, error) {
	if r.first == nil {
		return nil, io.EOF
	}
	f := r.first
	r.first = nil
	return f, nil
}

func TestReplicationStreamsOnlyFromTheDatabasesStandby(t *testing.T) {
	rt := &fakeDatabaseRuntime{}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	open := func(cn, id string) error {
		return s.DatabaseReplication(&replicationStream{ctx: tlsCaller(cn), first: &vmmdpb.DatabaseSessionFrame{DatabaseId: id}})
	}
	if err := open("db-2.faas", replicaDB); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("no standby attached = %v, want PermissionDenied", err)
	}
	if _, err := s.DatabaseControl(unixCaller(), &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "replication", Replication: "async", StandbyNode: "db-2"}); err != nil {
		t.Fatal(err)
	}
	for _, cn := range []string{"db-3.faas", "schedd.faas", "pgproxyd.faas"} {
		if err := open(cn, replicaDB); status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s = %v, want PermissionDenied", cn, err)
		}
	}
	other := "0f8fad5b-d9cb-469f-a165-70867728950e"
	if err := open("db-2.faas", other); status.Code(err) != codes.PermissionDenied {
		t.Errorf("the standby's node reached another database: %v", err)
	}
	if len(rt.calls) != 0 {
		t.Fatalf("refused streams reached the runtime: %v", rt.calls)
	}
	if err := open("db-2.faas", replicaDB); err != nil {
		t.Fatalf("the standby's node refused: %v", err)
	}
	if len(rt.calls) != 1 || rt.calls[0] != "replication "+replicaDB {
		t.Fatalf("runtime calls = %v", rt.calls)
	}
}

// A standby boot on one node reaches its primary's guest on another
// through DatabaseReplication.
func TestStandbyBootStreamsThroughThePrimarysNode(t *testing.T) {
	primaryRT := &fakeDatabaseRuntime{}
	primary := New(vmmStubBase{}, nil, "test", nil).WithDatabases(primaryRT)
	lis := bufconn.Listen(1 << 20)
	gsrv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(gsrv, primary)
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(gsrv.Stop)
	var dialed []string
	standbyRT := &fakeDatabaseRuntime{}
	standby := New(vmmStubBase{}, nil, "test", nil).WithDatabases(standbyRT).WithDatabasePeers(func(ctx context.Context, target, nodeCN string) (*grpc.ClientConn, error) {
		dialed = append(dialed, target+" "+nodeCN)
		return grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	ctx := unixCaller()
	start := &vmmdpb.StartDatabaseRequest{DatabaseId: replicaDB, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 512,
		PrimaryNode: "db-1", PrimaryTarget: "tcp://10.0.0.1:9101"}
	if _, err := standby.StartDatabase(ctx, start); err != nil {
		t.Fatal(err)
	}
	if standbyRT.started.Primary == nil {
		t.Fatal("a standby boot carried no way to its primary")
	}
	if _, err := primary.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "replication", Replication: "async", StandbyNode: "db-2"}); err != nil {
		t.Fatal(err)
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := standbyRT.started.Primary(dialCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Write([]byte("START_REPLICATION")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("START_REPLICATION"))
	if _, err := io.ReadFull(stream, got); err != nil || string(got) != "START_REPLICATION" {
		t.Fatalf("echo through the primary's node = %q, %v", got, err)
	}
	if len(dialed) != 1 || dialed[0] != "tcp://10.0.0.1:9101 db-1.faas" {
		t.Fatalf("dialed %v; want the primary's target, verified as db-1.faas", dialed)
	}

	for name, req := range map[string]*vmmdpb.StartDatabaseRequest{
		"node without target": {DatabaseId: replicaDB, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 512, PrimaryNode: "db-1"},
		"standby restore": {DatabaseId: replicaDB, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 512, PrimaryNode: "db-1", PrimaryTarget: "tcp://x:1",
			RestoreSource: "0f8fad5b-d9cb-469f-a165-70867728950e", RestoreTarget: timestamppb.Now()},
		"promote with a primary": {DatabaseId: replicaDB, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 512, PrimaryNode: "db-1", PrimaryTarget: "tcp://x:1", Promote: true},
		"promote a restore": {DatabaseId: replicaDB, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 512, Promote: true,
			RestoreSource: "0f8fad5b-d9cb-469f-a165-70867728950e", RestoreTarget: timestamppb.Now()},
	} {
		if _, err := standby.StartDatabase(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s = %v, want InvalidArgument", name, err)
		}
	}
	noPeers := New(vmmStubBase{}, nil, "test", nil).WithDatabases(&fakeDatabaseRuntime{})
	if _, err := noPeers.StartDatabase(ctx, start); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("a node without a peer identity booted a standby: %v", err)
	}
}

// peerVerifier is a database node's handshake verifier: schedd, pgproxyd
// and its databases' current standby nodes (cmd/vmmd builds the same).
type peerVerifier struct{ server *Server }

func (v peerVerifier) LookupCN(cn string) error {
	if cn == "schedd.faas" || cn == "pgproxyd.faas" || v.server.AdmitsDatabasePeer(cn) {
		return nil
	}
	return wire.ErrNodeVerifierCNMismatch
}

// issueLeaf writes a CA-signed leaf for cn and returns its paths.
func issueLeaf(t *testing.T, root string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, kind pki.RoleKind, name, cn string) (string, string) {
	t.Helper()
	role := pki.Role{CommonName: "vmmd.faas", Kind: kind, Directory: "nodes", Filename: name, AltNames: pki.ProductionSANs("vmmd.faas")}
	if err := pki.EnsureLeafWithCNAndSANs(root, role, cn, ca, caKey, true, pki.AltNames{IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}); err != nil {
		t.Fatal(err)
	}
	return pki.LeafPaths(root, role)
}

// The peer hop over real mutual TLS: the standby's node proves its node
// identity, the primary's node proves its own, and only the node schedd
// named as the database's standby gets a stream.
func TestStandbyStreamsOverMutualTLSOnlyAsItsNode(t *testing.T) {
	root := t.TempDir()
	ca, caKey, err := pki.EnsureCA(root, false)
	if err != nil {
		t.Fatal(err)
	}
	caPath, _ := pki.CARoot(root)
	serverCert, serverKey := issueLeaf(t, root, ca, caKey, pki.KindServer, "db-1-server", "db-1.faas")
	primary := New(vmmStubBase{}, nil, "test", nil).WithDatabases(&fakeDatabaseRuntime{})
	serverTLS, err := wire.LoadServerTLSConfigWithVerifier(serverCert, serverKey, caPath, peerVerifier{primary})
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer(wire.ServerCredsOrEmpty(serverTLS)...)
	vmmdpb.RegisterVmmdServer(gsrv, primary)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(gsrv.Stop)
	target := "tcp://" + lis.Addr().String()
	if _, err := primary.DatabaseControl(unixCaller(), &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "replication", Replication: "async", StandbyNode: "db-2"}); err != nil {
		t.Fatal(err)
	}

	stream := func(node, primaryNode string) error {
		cert, key := issueLeaf(t, root, ca, caKey, pki.KindClient, node+"-peer", node+".faas")
		dial, err := NewDatabasePeerDialer(cert, key, caPath)
		if err != nil {
			return err
		}
		rt := &fakeDatabaseRuntime{}
		s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt).WithDatabasePeers(dial)
		if _, err := s.StartDatabase(unixCaller(), &vmmdpb.StartDatabaseRequest{DatabaseId: replicaDB, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 512,
			PrimaryNode: primaryNode, PrimaryTarget: target}); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := rt.started.Primary(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.Write([]byte("IDENTIFY_SYSTEM")); err != nil {
			return err
		}
		got := make([]byte, len("IDENTIFY_SYSTEM"))
		if _, err := io.ReadFull(conn, got); err != nil {
			return err
		}
		if string(got) != "IDENTIFY_SYSTEM" {
			return fmt.Errorf("echo %q", got)
		}
		return nil
	}
	if err := stream("db-2", "db-1"); err != nil {
		t.Fatalf("the standby's node: %v", err)
	}
	if err := stream("db-3", "db-1"); err == nil {
		t.Fatal("another database node streamed the database")
	} else {
		t.Logf("another node: %v", err)
	}
	if err := stream("db-2", "db-9"); err == nil {
		t.Fatal("the standby trusted a primary node with another identity")
	} else {
		t.Logf("wrong primary identity: %v", err)
	}
	if _, err := primary.DatabaseControl(unixCaller(), &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "replication", Replication: "off"}); err != nil {
		t.Fatal(err)
	}
	if err := stream("db-2", "db-1"); err == nil {
		t.Fatal("a detached standby's node still streamed")
	} else {
		t.Logf("detached standby: %v", err)
	}
}

// A failover while parked boots the standby volume with no primary and
// promotes it.
func TestPromotionBootReachesTheRuntime(t *testing.T) {
	rt := &fakeDatabaseRuntime{}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	if _, err := s.StartDatabase(unixCaller(), &vmmdpb.StartDatabaseRequest{DatabaseId: replicaDB, PostgresMajor: 17, VcpuCount: 1, MemSizeMib: 512, Promote: true}); err != nil {
		t.Fatal(err)
	}
	if !rt.started.Promote || rt.started.Primary != nil {
		t.Fatalf("boot request %+v; want a promotion without a primary", rt.started)
	}
}

// An import's endpoint stays with the host; the guest gets the login and
// target only.
func TestImportControlSplitsEndpointAndLogin(t *testing.T) {
	started := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateReady,
		Import: &dbguest.ImportStatus{State: dbguest.ImportRunning, StartedAt: started}}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	ctx := unixCaller()
	in := &vmmdpb.DatabaseImport{Host: "ep-quiet-1.us-east-2.aws.neon.tech", Port: 5432, Database: "gregale", User: "gregale_mig_ab",
		Password: "never-logged", TargetDatabase: "gregale", Owner: "gregale_schema_owner", SkipExtensions: []string{"neon"},
		OperationId: "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"}
	resp, err := s.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "import", Import: in})
	if err != nil || resp.GetImport().GetState() != "running" || !resp.GetImport().GetStartedAt().AsTime().Equal(started) {
		t.Fatalf("import = %+v, %v", resp, err)
	}
	if len(rt.calls) != 1 || rt.calls[0] != "import_source "+replicaDB+" ep-quiet-1.us-east-2.aws.neon.tech:5432" {
		t.Fatalf("runtime calls %v", rt.calls)
	}
	got := rt.controls[len(rt.controls)-1]
	if got.Op != dbguest.OpImport || got.Import == nil || got.Import.User != "gregale_mig_ab" || got.Import.Password != "never-logged" ||
		got.Import.Owner != "gregale_schema_owner" || len(got.Import.SkipExtensions) != 1 || got.Import.OperationID != in.OperationId {
		t.Fatalf("guest request %+v", got)
	}
	for name, req := range map[string]*vmmdpb.DatabaseControlRequest{
		"no source":        {DatabaseId: replicaDB, Op: "import"},
		"source on status": {DatabaseId: replicaDB, Op: "status", Import: in},
		"bad host": {DatabaseId: replicaDB, Op: "import", Import: &vmmdpb.DatabaseImport{Host: "a b", Port: 5432, Database: "gregale", User: "u",
			Password: "p", TargetDatabase: "gregale", Owner: "o"}},
	} {
		if _, err := s.DatabaseControl(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s = %v, want InvalidArgument", name, err)
		}
	}
}

// A failed import reports the extensions the target lacks, and vmmd
// forwards only plain identifiers whatever the guest says.
func TestImportStatusForwardsOnlyReportableExtensions(t *testing.T) {
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateReady,
		Import: &dbguest.ImportStatus{State: dbguest.ImportFailed, Error: dbguest.ErrCodeExtension,
			MissingExtensions: []string{"pg_stat_statements", "password=leaked"}, RequiredVolumeBytes: 5 << 30}}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	resp, err := s.DatabaseControl(unixCaller(), &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "import_status"})
	if err != nil || resp.GetImport().GetErrorCode() != dbguest.ErrCodeExtension ||
		!slices.Equal(resp.GetImport().GetMissingExtensions(), []string{"pg_stat_statements"}) || resp.GetImport().GetRequiredVolumeBytes() != 5<<30 {
		t.Fatalf("import status = %+v, %v", resp, err)
	}
}

// A ready guest's storage reaches schedd as reported.
func TestStatusForwardsStorage(t *testing.T) {
	rt := &fakeDatabaseRuntime{controlRs: dbguest.Response{State: dbguest.StateReady,
		Storage: &dbguest.StorageStatus{TotalBytes: 1 << 30, FreeBytes: 100 << 20, ReadOnly: true, Low: true}}}
	s := New(vmmStubBase{}, nil, "test", nil).WithDatabases(rt)
	resp, err := s.DatabaseControl(unixCaller(), &vmmdpb.DatabaseControlRequest{DatabaseId: replicaDB, Op: "status"})
	st := resp.GetStorage()
	if err != nil || st.GetTotalBytes() != 1<<30 || st.GetFreeBytes() != 100<<20 || !st.GetReadOnly() || !st.GetLow() {
		t.Fatalf("status = %+v, %v", resp, err)
	}
}
