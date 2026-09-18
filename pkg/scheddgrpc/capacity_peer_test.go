package scheddgrpc_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"sync"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type capacityPeerResolver map[string]string

func (r capacityPeerResolver) NodeIDByCN(cn string) (string, error) {
	if id := r[cn]; id != "" {
		return id, nil
	}
	return "", errors.New("unknown peer CN")
}

type capacityStream struct {
	grpc.ServerStream
	ctx  context.Context
	msgs []*scheddpb.CapacityReport
	idx  int
	ack  *scheddpb.ReportCapacityAck
}

func (s *capacityStream) Context() context.Context { return s.ctx }

func (s *capacityStream) Recv() (*scheddpb.CapacityReport, error) {
	if s.idx >= len(s.msgs) {
		return nil, io.EOF
	}
	msg := s.msgs[s.idx]
	s.idx++
	return msg, nil
}

func (s *capacityStream) SendAndClose(ack *scheddpb.ReportCapacityAck) error {
	s.ack = ack
	return nil
}

func mtlsPeerContext(cn string) context.Context {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}},
	}})
}

func TestReportCapacity_BindsPeerCNToReportedNode(t *testing.T) {
	var (
		mu       sync.Mutex
		received []sched.CapacityReport
	)
	engine := &capturingEngine{mu: &mu, recv: &received}
	server := scheddgrpc.New(engine, nil, nil).WithPeerNodeResolver(capacityPeerResolver{
		"compute-a": "node-uuid-a",
	})

	stream := &capacityStream{
		ctx: mtlsPeerContext("compute-a"),
		msgs: []*scheddpb.CapacityReport{{
			NodeId:          "node-uuid-a",
			SampledAtUnixMs: 1730000000000,
			LiveCount:       1,
		}},
	}
	if err := server.ReportCapacity(stream); err != nil {
		t.Fatalf("ReportCapacity: %v", err)
	}
	if stream.ack == nil {
		t.Fatal("SendAndClose was not called")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0].NodeID != "node-uuid-a" {
		t.Fatalf("received = %+v, want one report for node-uuid-a", received)
	}
}

func TestReportCapacity_RejectsPeerNodeMismatch(t *testing.T) {
	var (
		mu       sync.Mutex
		received []sched.CapacityReport
	)
	engine := &capturingEngine{mu: &mu, recv: &received}
	server := scheddgrpc.New(engine, nil, nil).WithPeerNodeResolver(capacityPeerResolver{
		"compute-a": "node-uuid-a",
	})

	stream := &capacityStream{
		ctx: mtlsPeerContext("compute-a"),
		msgs: []*scheddpb.CapacityReport{{
			NodeId:          "node-uuid-b",
			SampledAtUnixMs: 1730000000000,
		}},
	}
	err := server.ReportCapacity(stream)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("error code = %v, want Unauthenticated (err=%v)", status.Code(err), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 0 {
		t.Fatalf("received = %+v, want no report after identity mismatch", received)
	}
}
