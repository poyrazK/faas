package vmmdgrpc

import (
	"context"
	"io"
	"regexp"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Replication between database nodes (ADR-1027 production class). A
// standby's node dials its primary's node with its own node-identity
// client certificate (<node>.faas) and opens a DatabaseReplication stream
// per replication connection. The primary's node admits a stream only from
// the node schedd named as that database's standby, through the
// replication control op, and only for that database.

// DatabasePeerDialer dials another database node's vmmd at target,
// authenticating as this node and requiring the server's certificate to
// carry nodeCN, the other node's identity.
type DatabasePeerDialer func(ctx context.Context, target, nodeCN string) (*grpc.ClientConn, error)

// NewDatabasePeerDialer dials other database nodes' vmmd with this node's
// peer client certificate, requiring each server's certificate to carry
// the node identity the caller names. It loads the certificate once to
// fail early.
func NewDatabasePeerDialer(certPath, keyPath, caPath string) (DatabasePeerDialer, error) {
	if _, err := wire.LoadClientTLSConfigWithPrefix("database_vms.peer_tls", certPath, keyPath, caPath); err != nil {
		return nil, err
	}
	return func(ctx context.Context, target, nodeCN string) (*grpc.ClientConn, error) {
		node := wire.NewInmemNodeVerifier()
		node.Set([]string{nodeCN})
		tlsCfg, err := wire.LoadClientTLSConfigWithPrefixAndVerifier("database_vms.peer_tls", certPath, keyPath, caPath, node)
		if err != nil {
			return nil, err
		}
		return overlay.Dial(ctx, overlay.New(target), tlsCfg)
	}, nil
}

type databasePeers struct {
	mu sync.Mutex
	// standbys maps a database on this node to its standby's node CN.
	standbys map[string]string
	dial     DatabasePeerDialer
	conns    map[string]*grpc.ClientConn
}

// WithDatabasePeers lets standby boots on this node reach their primaries.
func (s *Server) WithDatabasePeers(dial DatabasePeerDialer) *Server {
	s.databasePeers.mu.Lock()
	s.databasePeers.dial = dial
	s.databasePeers.mu.Unlock()
	return s
}

// AdmitsDatabasePeer reports whether cn is the node of some database's
// standby here, for the TLS handshake. DatabaseReplication then binds the
// caller to that database.
func (s *Server) AdmitsDatabasePeer(cn string) bool {
	p := &s.databasePeers
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, standby := range p.standbys {
		if standby == cn {
			return true
		}
	}
	return false
}

var databaseNodeCNPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// databaseNodeCN is the certificate identity of a database node id, as
// gregalectl issues it (<id>.faas).
func databaseNodeCN(node string) (string, error) {
	cn := strings.TrimSpace(node)
	if !strings.HasSuffix(cn, ".faas") {
		cn += ".faas"
	}
	if len(cn) > 253 || !databaseNodeCNPattern.MatchString(cn) {
		return "", status.Error(codes.InvalidArgument, "invalid database node")
	}
	return cn, nil
}

// noteStandby records which node may stream a database's replication from
// here: the replication op sets it with async or sync and forgets it with
// off.
func (p *databasePeers) noteStandby(id string, req dbguest.Request, standbyNode string) error {
	if req.Op != dbguest.OpReplication || req.Replication == "" || req.Replication == dbguest.ReplicationOff {
		if standbyNode != "" {
			return status.Error(codes.InvalidArgument, "standby_node goes with replication async or sync")
		}
		if req.Op == dbguest.OpReplication && req.Replication == dbguest.ReplicationOff {
			p.forget(id)
		}
		return nil
	}
	if standbyNode == "" {
		return status.Error(codes.InvalidArgument, "replication async and sync name the standby's node")
	}
	cn, err := databaseNodeCN(standbyNode)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.standbys == nil {
		p.standbys = map[string]string{}
	}
	p.standbys[id] = cn
	return nil
}

func (p *databasePeers) forget(id string) {
	p.mu.Lock()
	delete(p.standbys, id)
	p.mu.Unlock()
}

func (p *databasePeers) standby(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cn, ok := p.standbys[id]
	return cn, ok
}

// DatabaseReplication relays one standby replication stream to the
// primary's guest.
func (s *Server) DatabaseReplication(stream vmmdpb.Vmmd_DatabaseReplicationServer) error {
	ctx := stream.Context()
	if s.databases == nil {
		return status.Error(codes.Unimplemented, "database VMs are not enabled on this node")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	id := first.GetDatabaseId()
	standby, ok := s.databasePeers.standby(id)
	if !ok {
		return status.Error(codes.PermissionDenied, "database rpc: no standby is attached to this database")
	}
	if err := requireDatabaseCaller(ctx, standby); err != nil {
		return err
	}
	conn, err := s.databases.DialReplication(ctx, id)
	if err != nil {
		return s.databaseStatus("replication", err)
	}
	defer func() { _ = conn.Close() }()
	spliceFrames(stream, conn, first.GetData())
	return nil
}

// dialer returns the standby boot's way to its primary: one
// DatabaseReplication stream per replication connection, over a
// connection to the primary's node kept for later streams.
func (p *databasePeers) dialer(id, primaryNode, target string) (dbvm.ReplicationDialer, error) {
	p.mu.Lock()
	dial := p.dial
	p.mu.Unlock()
	if dial == nil {
		return nil, status.Error(codes.FailedPrecondition, "this node has no database peer identity")
	}
	cn, err := databaseNodeCN(primaryNode)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (io.ReadWriteCloser, error) {
		conn, err := p.conn(ctx, dial, target, cn)
		if err != nil {
			return nil, err
		}
		// The stream lives as long as the replication connection.
		streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		stream, err := vmmdpb.NewVmmdClient(conn).DatabaseReplication(streamCtx)
		if err == nil {
			err = stream.Send(&vmmdpb.DatabaseSessionFrame{DatabaseId: id})
		}
		if err != nil {
			cancel()
			return nil, err
		}
		return &frameConn{stream: stream, closeSend: stream.CloseSend, cancel: cancel}, nil
	}, nil
}

func (p *databasePeers) conn(ctx context.Context, dial DatabasePeerDialer, target, cn string) (*grpc.ClientConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := cn + "|" + target
	if conn, ok := p.conns[key]; ok {
		return conn, nil
	}
	conn, err := dial(ctx, target, cn)
	if err != nil {
		return nil, err
	}
	if p.conns == nil {
		p.conns = map[string]*grpc.ClientConn{}
	}
	p.conns[key] = conn
	return conn, nil
}

// frameConn adapts a client frame stream to io.ReadWriteCloser.
type frameConn struct {
	stream    frameStream
	closeSend func() error
	cancel    context.CancelFunc
	pending   []byte
	writeMu   sync.Mutex
}

const maxReplicationFrame = 64 << 10

func (c *frameConn) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		frame, err := c.stream.Recv()
		if err != nil {
			return 0, io.EOF
		}
		c.pending = frame.GetData()
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *frameConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for written < len(p) {
		end := min(written+maxReplicationFrame, len(p))
		if err := c.stream.Send(&vmmdpb.DatabaseSessionFrame{Data: append([]byte(nil), p[written:end]...)}); err != nil {
			return written, err
		}
		written = end
	}
	return written, nil
}

func (c *frameConn) Close() error {
	_ = c.closeSend()
	c.cancel()
	return nil
}
