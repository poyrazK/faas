// adr: 1027
package pgproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// echoVmmd answers DatabaseSession by echoing every data frame and records
// the database the session named.
type echoVmmd struct {
	vmmdpb.UnimplementedVmmdServer
	database string
}

func (e *echoVmmd) DatabaseSession(stream vmmdpb.Vmmd_DatabaseSessionServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	e.database = first.GetDatabaseId()
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&vmmdpb.DatabaseSessionFrame{Data: frame.GetData()}); err != nil {
			return err
		}
	}
}

func bufDial(t *testing.T, register func(*grpc.Server)) func(context.Context, string, *tls.Config) (*grpc.ClientConn, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gsrv := grpc.NewServer()
	register(gsrv)
	go func() { _ = gsrv.Serve(lis) }()
	t.Cleanup(gsrv.Stop)
	return func(context.Context, string, *tls.Config) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// TestVmmdDialerStreamsLargeWrites sends more than one frame's worth of
// bytes through the session and reads them back intact.
func TestVmmdDialerStreamsLargeWrites(t *testing.T) {
	srv := &echoVmmd{}
	d := NewVmmdDialerWith(nil, bufDial(t, func(g *grpc.Server) { vmmdpb.RegisterVmmdServer(g, srv) }))
	defer func() { _ = d.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	session, err := d.Dial(ctx, "unix:///run/faas/vmmd.sock", "0f8fad5b-d9cb-469f-a165-70867728950e")
	cancel() // setup context ending must not end the session
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	payload := bytes.Repeat([]byte("0123456789abcdef"), (3*maxSessionFrame)/16+7)
	go func() { _, _ = session.Write(payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(session, got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload corrupted in transit")
	}
	if srv.database != "0f8fad5b-d9cb-469f-a165-70867728950e" {
		t.Fatalf("session named %q", srv.database)
	}
}

type fakeSchedd struct {
	scheddpb.UnimplementedScheddServer
	target  string
	touched string
}

func (f *fakeSchedd) WakeDatabase(_ context.Context, req *scheddpb.DatabaseRef) (*scheddpb.DatabaseRuntime, error) {
	return &scheddpb.DatabaseRuntime{DatabaseId: req.GetDatabaseId(), State: "running", VmmdTarget: f.target}, nil
}

func (f *fakeSchedd) TouchDatabase(_ context.Context, req *scheddpb.DatabaseRef) (*scheddpb.TouchDatabaseResponse, error) {
	f.touched = req.GetDatabaseId()
	return &scheddpb.TouchDatabaseResponse{}, nil
}

func TestScheddWaker(t *testing.T) {
	srv := &fakeSchedd{target: "tcp://10.0.2.10:7443"}
	conn, err := bufDial(t, func(g *grpc.Server) { scheddpb.RegisterScheddServer(g, srv) })(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	w := NewScheddWaker(scheddpb.NewScheddClient(conn))
	if target, err := w.Wake(context.Background(), "db"); err != nil || target != "tcp://10.0.2.10:7443" {
		t.Fatalf("Wake = %q, %v", target, err)
	}
	if err := w.Touch(context.Background(), "db"); err != nil || srv.touched != "db" {
		t.Fatalf("Touch = %v (touched %q)", err, srv.touched)
	}
	srv.target = ""
	if _, err := w.Wake(context.Background(), "db"); err == nil {
		t.Fatal("a wake without a vmmd target succeeded")
	}
}
