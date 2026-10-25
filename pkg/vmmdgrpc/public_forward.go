package vmmdgrpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type publicForwardVMM interface {
	PublicForwardTarget(string) (*netns.PublicForwardTarget, error)
}

type publicForwardContextKey struct{}

func publicTarget(ctx context.Context) *netns.PublicForwardTarget {
	target, _ := ctx.Value(publicForwardContextKey{}).(*netns.PublicForwardTarget)
	return target
}

func streamBridgeEntryKey(instance string, target *netns.PublicForwardTarget) string {
	if target != nil {
		return instance + "#public" + strconv.Itoa(target.IfIndex)
	}
	return instance
}

func samePublicTarget(a, b *netns.PublicForwardTarget) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func withPublicTarget(ctx context.Context, target *netns.PublicForwardTarget) context.Context {
	return context.WithValue(ctx, publicForwardContextKey{}, target)
}

func (s *Server) publicForwardContext(ctx context.Context, instance string) (context.Context, error) {
	if wire.IsPrivateForward(ctx) {
		return ctx, nil
	}
	vmm, ok := s.vmm.(publicForwardVMM)
	if !ok { // non-native/test implementations retain the namespace bridge
		return ctx, nil
	}
	target, err := vmm.PublicForwardTarget(instance)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "public forwarding: %v", err)
	}
	if target != nil {
		if err := target.Validate(); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "public forwarding: %v", err)
		}
	}
	return withPublicTarget(ctx, target), nil
}

// namespaceOrPublicCommand launches a raw, TCP or UDP bridge. A namespace
// bridge runs as id (connection-survival ADR, slice 1a); a public-target
// bridge always has the root identity (forwardBridgeIdentity).
func namespaceOrPublicCommand(ctx context.Context, id bridgeIdentity, pid int, bridgePath string, port uint32) *exec.Cmd {
	if target := publicTarget(ctx); target != nil {
		cmd := exec.CommandContext(ctx, bridgePath, target.HostIP.String(), strconv.FormatUint(uint64(port), 10))
		cmd.Env = netns.PublicForwardEnv(os.Environ(), target)
		cmd.SysProcAttr = streamBridgeSysProcAttr()
		return cmd
	}
	args := append(id.nsenterArgs("--target", strconv.Itoa(pid), "--net"),
		bridgePath, netns.GuestIP, strconv.FormatUint(uint64(port), 10))
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = netns.PublicForwardEnv(id.env(), nil)
	return cmd
}

func streamBridgeTarget(ctx context.Context, netnsName string) (string, string) {
	if target := publicTarget(ctx); target != nil {
		return "", target.HostIP.String()
	}
	return netnsName, netns.GuestIP
}

func legacyBridgeCommand(ctx context.Context, req *vmmdpb.ForwardHTTPRequestInit, netnsName string, timeout time.Duration) (*exec.Cmd, *os.File, error) {
	target := publicTarget(ctx)
	if target == nil {
		cmd := exec.CommandContext(ctx, "ip", "netns", "exec", netnsName, "bash", "-c", buildStreamingBridgeScript(req, timeout))
		cmd.Env = netns.PublicForwardEnv(os.Environ(), nil)
		return cmd, nil, nil
	}
	port := req.GetPort()
	if port == 0 {
		port = netns.AppPort
	}
	conn, err := target.DialContext(ctx, &net.Dialer{Timeout: 30 * time.Second}, "tcp", net.JoinHostPort(target.HostIP.String(), strconv.FormatUint(uint64(port), 10)))
	if err != nil {
		return nil, nil, err
	}
	fd, err := conn.(*net.TCPConn).File()
	_ = conn.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("public bridge descriptor: %w", err)
	}
	cmd := exec.CommandContext(ctx, "bash", "-c", buildStreamingBridgeScript(req, timeout, true))
	cmd.Env = netns.PublicForwardEnv(os.Environ(), nil)
	cmd.ExtraFiles = []*os.File{fd}
	cmd.SysProcAttr = streamBridgeSysProcAttr()
	return cmd, fd, nil
}
