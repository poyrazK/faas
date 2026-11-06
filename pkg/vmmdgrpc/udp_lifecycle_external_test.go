// adr: 20261105115100
package vmmdgrpc_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The fake only substitutes namespace entry and the guest bridge's bytes.
// The production RPC handler, command contexts and Destroy path run unchanged.
type auditLifecycleVMM struct {
	vmmdgrpc.VmmdAPI
	live atomic.Bool
}

func (v *auditLifecycleVMM) NetnsFor(string) (string, bool) { return "audit-ns", v.live.Load() }
func (v *auditLifecycleVMM) InstancePID(string) (int, bool) { return os.Getpid(), v.live.Load() }
func (v *auditLifecycleVMM) DestroyWithExport(context.Context, string, string) (int, error) {
	v.live.Store(false)
	return 0, nil
}

type auditUDPStream struct {
	grpc.ServerStream
	ctx         context.Context
	initialized bool
	ready       chan struct{}
}

func (s *auditUDPStream) Context() context.Context { return s.ctx }
func (s *auditUDPStream) Recv() (*vmmdpb.ForwardUDPRequest, error) {
	if !s.initialized {
		s.initialized = true
		return &vmmdpb.ForwardUDPRequest{Frame: &vmmdpb.ForwardUDPRequest_Init{Init: &vmmdpb.ForwardUDPRequestInit{Instance: "audit-instance", Port: 5353}}}, nil
	}
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
func (s *auditUDPStream) Send(frame *vmmdpb.ForwardUDPResponse) error {
	if frame.GetInit() != nil {
		if frame.GetInit().GetError() != "" {
			return errors.New(frame.GetInit().GetError())
		}
		close(s.ready)
	}
	return nil
}

type auditTCPStream struct {
	grpc.ServerStream
	ctx         context.Context
	initialized bool
	ready       chan struct{}
}

func (s *auditTCPStream) Context() context.Context { return s.ctx }
func (s *auditTCPStream) Recv() (*vmmdpb.ForwardTCPRequest, error) {
	if !s.initialized {
		s.initialized = true
		return &vmmdpb.ForwardTCPRequest{Frame: &vmmdpb.ForwardTCPRequest_Init{Init: &vmmdpb.ForwardTCPRequestInit{Instance: "audit-instance", Port: 9000}}}, nil
	}
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
func (s *auditTCPStream) Send(frame *vmmdpb.ForwardTCPResponse) error {
	if frame.GetInit() != nil {
		if frame.GetInit().GetError() != "" {
			return errors.New(frame.GetInit().GetError())
		}
		close(s.ready)
	}
	return nil
}

func TestForwardStreamsEndOnInstanceDestroy(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			directory := t.TempDir()
			bridge := filepath.Join(directory, "bridge")
			if err := os.WriteFile(bridge, []byte("#!/bin/sh\nprintf 'OK\\n' >&3\nexec cat\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "nsenter"), []byte("#!/bin/sh\nbridge_dir=${0%/*}\nexec \"$bridge_dir/bridge\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FAAS_VMMD_TCP_BRIDGE_PATH", bridge)
			t.Setenv("FAAS_VMMD_UDP_BRIDGE_PATH", bridge)
			t.Setenv("FAAS_INSTANCE_BRIDGE_SESSIONS", "0")
			t.Setenv("FAAS_BRIDGE_UNPRIVILEGED", "0")
			vmm := &auditLifecycleVMM{}
			vmm.live.Store(true)
			server := vmmdgrpc.New(vmm, nil, "audit", nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ready := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				if protocol == "tcp" {
					done <- server.ForwardTCPStream(&auditTCPStream{ctx: ctx, ready: ready}) //nolint:contextcheck // RPCs receive ctx through the stream's Context method.
				} else {
					done <- server.ForwardUDPStream(&auditUDPStream{ctx: ctx, ready: ready}) //nolint:contextcheck // RPCs receive ctx through the stream's Context method.
				}
			}()
			select {
			case <-ready:
			case err := <-done:
				t.Fatalf("bridge failed before teardown: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("bridge never became ready")
			}
			if _, err := server.Destroy(ctx, &vmmdpb.DestroyRequest{Instance: "audit-instance"}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if status.Code(err) != codes.Unavailable {
					t.Fatalf("%s shutdown status: %v", protocol, err)
				}
			case <-time.After(time.Second):
				t.Errorf("%s stream and per-connection helper remain active after Destroy returned", protocol)
				cancel()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("stream cleanup did not finish")
				}
			}
		})
	}
}
