//go:build linux && metal

// spec: §14.A
// adr: 1530
package m9

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

//go:embed native_m9_peer.py
var peerProgram string

type nativePeer struct {
	config      Peer
	runID       string
	nodeID      string
	incarnation string
	scheddSHA   string
	claimed     bool
	leaks       map[string]string
	conn        *grpc.ClientConn
	client      vmmdpb.VmmdClient
	tunnel      *exec.Cmd
	cancel      context.CancelFunc
	sockDir     string
}

func (p *nativePeer) action(ctx context.Context, action string) (map[string]any, error) {
	request := map[string]any{"run_id": p.runID, "vmmd_sha256": p.config.VMMDSHA256, "action": action}
	if p.scheddSHA != "" {
		request["schedd_sha256"] = p.scheddSHA
	}
	if action == "leaks" || action == "release" {
		request["leak_files"] = p.leaks
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	payload := base64.StdEncoding.EncodeToString(raw)
	// Only validated SSH aliases and a base64 document enter the remote shell.
	// The program itself is the reviewed checkout's fixed stdin payload.
	cmd := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		p.config.SSH, "sudo", "-n", "python3", "-", payload)
	cmd.Stdin = bytes.NewBufferString(peerProgram)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("M9 peer %s %s failed: %w: %s", p.config.Name, action, err, stderr.String())
	}
	var result map[string]any
	if len(output) > 65536 || json.Unmarshal(output, &result) != nil {
		return nil, fmt.Errorf("M9 peer %s returned invalid evidence", p.config.Name)
	}
	return result, nil
}

func (p *nativePeer) connect(ctx context.Context, intercept grpc.UnaryClientInterceptor) error {
	dir, err := os.MkdirTemp("", "m9-sock-")
	if err != nil {
		return err
	}
	p.sockDir = dir
	socket := filepath.Join(dir, "vmmd.sock")
	tunnelCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.tunnel = exec.CommandContext(tunnelCtx, "ssh", "-T", "-N",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ExitOnForwardFailure=yes",
		"-L", socket+":"+p.config.Socket, p.config.SSH)
	if err := p.tunnel.Start(); err != nil {
		return fmt.Errorf("M9 SSH forwarding: %w", err)
	}
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}),
	}
	if intercept != nil {
		options = append(options, grpc.WithUnaryInterceptor(intercept))
	}
	p.conn, err = grpc.NewClient("passthrough:///native-m9", options...)
	if err != nil {
		return err
	}
	p.client = vmmdpb.NewVmmdClient(p.conn)
	return p.waitReady(ctx)
}

func (p *nativePeer) waitReady(ctx context.Context) error {
	for {
		callCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := p.client.Ping(callCtx, &vmmdpb.PingRequest{})
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("M9 peer %s did not become ready: %w", p.config.Name, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (p *nativePeer) close() error {
	var err error
	if p.conn != nil {
		err = p.conn.Close()
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.tunnel != nil && p.tunnel.Process != nil {
		_ = p.tunnel.Wait()
	}
	if p.sockDir != "" {
		err = errors.Join(err, os.RemoveAll(p.sockDir))
	}
	return err
}
