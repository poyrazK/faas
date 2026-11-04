package vmmdgrpc

// vmmd's local data socket (ADR-20261030230157): the node's own
// gatewayd-internal forwards requests to vmmd over a unix socket instead of
// mTLS over the overlay, so neither side spends CPU on TLS for a hop that never
// leaves the host. The socket admits one uid, the gateway's, by peer
// credentials, and serves only the RPCs the gateway calls.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// localDataMethods are the RPCs gatewayd-internal calls on vmmd: request
// forwarding and the per-request lookups beside it. Every other RPC, among them
// the lifecycle and scheduler-only ones, is refused on the local data socket.
// TestLocalDataMethodsCoverTheGateway keeps the list in step with the gateway.
var localDataMethods = map[string]bool{
	vmmdpb.Vmmd_ForwardHTTPStream_FullMethodName:    true,
	vmmdpb.Vmmd_ForwardRawStream_FullMethodName:     true,
	vmmdpb.Vmmd_ForwardTCPStream_FullMethodName:     true,
	vmmdpb.Vmmd_ForwardUDPStream_FullMethodName:     true,
	vmmdpb.Vmmd_Ping_FullMethodName:                 true,
	vmmdpb.Vmmd_AllowResolvedEgress_FullMethodName:  true,
	vmmdpb.Vmmd_AttachAppTask_FullMethodName:        true,
	vmmdpb.Vmmd_InflightRequestTrace_FullMethodName: true,
	vmmdpb.Vmmd_Logs_FullMethodName:                 true,
	vmmdpb.Vmmd_ResolveServiceCaller_FullMethodName: true,
}

// LocalDataServerOptions are the grpc.Server options of the local data
// socket: peer-uid authentication admitting only uid, and only the gateway's
// RPCs.
func LocalDataServerOptions(uid int) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.Creds(localDataCreds{uid: uid, peerUID: connPeerUID}),
		grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if err := authorizeLocalData(info.FullMethod); err != nil {
				return nil, err
			}
			return handler(ctx, req)
		}),
		grpc.ChainStreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if err := authorizeLocalData(info.FullMethod); err != nil {
				return err
			}
			return handler(srv, ss)
		}),
	}
}

func authorizeLocalData(method string) error {
	if localDataMethods[method] {
		return nil
	}
	return status.Error(codes.PermissionDenied, "the local data socket serves only gatewayd-internal's forwarding RPCs")
}

// LocalDataPeer is the AuthInfo of a local data socket connection. It is
// never nil, so the unix-socket shortcuts of authorizeCaller and
// authorizeSchedulerPeer, which trust a peer without AuthInfo, never apply to
// this socket.
type LocalDataPeer struct {
	credentials.CommonAuthInfo
	UID int
}

// AuthType implements credentials.AuthInfo.
func (LocalDataPeer) AuthType() string { return "peercred" }

// localDataCreds admits a connection only from uid, read from the kernel's
// peer credentials before any byte of the connection is read.
type localDataCreds struct {
	uid     int
	peerUID func(net.Conn) (int, error)
}

func (c localDataCreds) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	uid, err := c.peerUID(conn)
	if err != nil {
		return nil, nil, fmt.Errorf("vmmd local data socket: peer credentials: %w", err)
	}
	if uid != c.uid {
		return nil, nil, fmt.Errorf("vmmd local data socket: uid %d may not connect", uid)
	}
	return conn, LocalDataPeer{CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.PrivacyAndIntegrity}, UID: uid}, nil
}

func (localDataCreds) ClientHandshake(context.Context, string, net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, errors.New("vmmd local data socket credentials are server-side only")
}

func (localDataCreds) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: "peercred"}
}

func (c localDataCreds) Clone() credentials.TransportCredentials { return c }

func (localDataCreds) OverrideServerName(string) error { return nil }

// connPeerUID reads the peer uid of a unix connection.
func connPeerUID(conn net.Conn) (int, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("%T has no file descriptor", conn)
	}
	return unixPeerUID(sc)
}
