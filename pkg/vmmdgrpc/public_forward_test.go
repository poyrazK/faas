package vmmdgrpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/metadata"
)

type publicForwardFakeVMM struct {
	forwardV2IntegrationVMM
	target *netns.PublicForwardTarget
	err    error
}

func (v publicForwardFakeVMM) PublicForwardTarget(string) (*netns.PublicForwardTarget, error) {
	return v.target, v.err
}

func testPublicForwardTarget(t *testing.T) *netns.PublicForwardTarget {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return &netns.PublicForwardTarget{HostIP: netip.MustParseAddr("127.0.0.1"), BridgeIP: netip.MustParseAddr("127.0.0.1"), Veth: iface.Name, IfIndex: iface.Index, AppPort: 8080}
		}
	}
	t.Fatal("no loopback interface")
	return nil
}

func TestPublicForwardContextFailsClosedAndPrivateIsExplicit(t *testing.T) {
	target := testPublicForwardTarget(t)
	s := New(publicForwardFakeVMM{target: target}, nil, "test", nil)
	defer func() { _ = s.Close(t.Context()) }()
	ctx, err := s.publicForwardContext(t.Context(), "instance")
	if err != nil || publicTarget(ctx) != target {
		t.Fatalf("public route missing: %v", err)
	}
	private := wire.ForwardContext(wire.WithPrivateForward(t.Context()))
	md, _ := metadata.FromOutgoingContext(private)
	ctx, err = s.publicForwardContext(metadata.NewIncomingContext(t.Context(), md), "instance")
	if err != nil || publicTarget(ctx) != nil {
		t.Fatalf("private route billed as public: %v", err)
	}
	s.vmm = publicForwardFakeVMM{err: errors.New("network retired")}
	if _, err := s.publicForwardContext(t.Context(), "instance"); err == nil {
		t.Fatal("retired public target fell back to namespace")
	}
}

func TestPublicForwardCommandsDoNotEnterGuestNamespace(t *testing.T) {
	ctx := withPublicTarget(t.Context(), testPublicForwardTarget(t))
	for _, bridge := range []string{"vmmd-raw-bridge", "vmmd-tcp-bridge", "vmmd-udp-bridge"} {
		cmd := namespaceOrPublicCommand(ctx, bridgeIdentity{}, 4242, "/opt/faas/current/bin/"+bridge, 9090)
		if cmd.Args[0] != "/opt/faas/current/bin/"+bridge || cmd.Args[1] != "127.0.0.1" || cmd.Args[2] != "9090" {
			t.Fatalf("public bridge entered namespace: %v", cmd.Args)
		}
		if !strings.Contains(strings.Join(cmd.Env, "\n"), "FAAS_PUBLIC_FORWARD_TARGET=") {
			t.Fatal("public helper lost original generation")
		}
	}
	cmd := namespaceOrPublicCommand(t.Context(), bridgeIdentity{}, 4242, "/opt/faas/current/bin/vmmd-tcp-bridge", 9090)
	if cmd.Args[0] != "nsenter" || strings.Contains(strings.Join(cmd.Env, "\n"), "FAAS_PUBLIC_FORWARD_TARGET=") {
		t.Fatalf("private namespace command: %v", cmd.Args)
	}
}

func TestPublicForwardLegacyRollbackUsesConnectedDescriptor(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("public output pin is a native Linux capability")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			t.Errorf("request URI = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, "funded legacy response")
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	parsed, _ := net.LookupPort("tcp", port)
	req := &vmmdpb.ForwardHTTPRequestInit{Instance: "instance", Method: http.MethodGet, RequestUri: "/stream", Port: uint32(parsed), Stream: true}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cmd, fd, err := legacyBridgeCommand(withPublicTarget(ctx, testPublicForwardTarget(t)), req, "fc-never-enter", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fd.Close() }()
	if cmd.Args[0] != "bash" || strings.Contains(cmd.Args[2], "/dev/tcp/") || len(cmd.ExtraFiles) != 1 {
		t.Fatalf("rollback can reopen an unmetered guest socket: %v", cmd.Args)
	}
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("funded legacy response")) {
		t.Fatalf("legacy public response: %q %v", out, err)
	}
}

func TestPublicForwardPersistentPoolsKeepPrivateAndGenerationSeparate(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(streamBridgePathEnv, path)
	manager := newTestStreamBridgeManager(t, func() {})
	type spawnRecord struct {
		namespace, ip, socket string
		env                   []string
	}
	var records []spawnRecord
	manager.spawn = func(ctx context.Context, _, namespace string, _ bridgeIdentity, socket, ip string, _ uint32, _ string, env []string) (*exec.Cmd, *bytes.Buffer, error) {
		records = append(records, spawnRecord{namespace, ip, socket, env})
		cmd := exec.CommandContext(ctx, "sleep", "60")
		if err := cmd.Start(); err != nil {
			return nil, nil, err
		}
		return cmd, &bytes.Buffer{}, nil
	}
	req := &vmmdpb.ForwardHTTPRequestInit{Instance: "instance-pools", Port: 8080}
	target := testPublicForwardTarget(t)
	acquire := func(ctx context.Context) {
		t.Helper()
		lease, err := manager.acquire(ctx, req, "fc-instance-pools", bridgeIdentity{})
		if err != nil {
			t.Fatal(err)
		}
		lease.release()
	}
	acquire(withPublicTarget(t.Context(), target))
	acquire(t.Context())
	acquire(withPublicTarget(t.Context(), target))
	if len(records) != 2 || len(manager.entries) != 2 || records[0].namespace != "" || records[0].ip != "127.0.0.1" || records[1].namespace != "fc-instance-pools" || records[0].socket == records[1].socket {
		t.Fatalf("public/private bridge collision: %+v", records)
	}
	replacement := *target
	replacement.IfIndex++
	acquire(withPublicTarget(t.Context(), &replacement))
	if len(records) != 3 {
		t.Fatal("replacement generation reused public bridge")
	}
	acquire(t.Context())
	if len(records) != 3 {
		t.Fatal("public replacement disturbed private bridge")
	}
	manager.forget(t.Context(), req.Instance)
	if len(manager.entries) != 0 {
		t.Fatal("teardown retained a public/private child")
	}
}
