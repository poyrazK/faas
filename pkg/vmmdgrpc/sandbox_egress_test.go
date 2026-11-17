// adr: 20261117091026 §3 — vmmd takes a session's domain policy only for a
// web-mode session and only as domain rules, puts it on the wake, and says
// at boot that it enforces policies.

package vmmdgrpc_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRunSandboxTakesTheEgressPolicy(t *testing.T) {
	vmm := newSandboxRunVMM()
	cli := newSandboxRunClient(t, vmm, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := cli.RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := sandboxRunStart()
	start.GetStart().NetworkMode = api.SandboxNetworkWeb
	start.GetStart().EgressAllow, start.GetStart().EgressDeny = []string{"example.com"}, []string{"admin.example.com"}
	if err := stream.Send(start); err != nil {
		t.Fatal(err)
	}
	booted := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetBooted() != nil })
	if !booted.GetBooted().GetEgressPolicy() {
		t.Fatal("vmmd did not say it enforces egress policies")
	}
	if w := vmm.lastWake(); !slices.Equal(w.EgressAllow, []string{"example.com"}) || !slices.Equal(w.EgressDeny, []string{"admin.example.com"}) {
		t.Fatalf("wake policy %v / %v", w.EgressAllow, w.EgressDeny)
	}
}

func TestRunSandboxRefusesAnInvalidEgressPolicy(t *testing.T) {
	for name, mutate := range map[string]func(*vmmdpb.RunSandboxStart){
		"registries mode": func(s *vmmdpb.RunSandboxStart) {
			s.NetworkMode, s.EgressAllow = api.SandboxNetworkRegistries, []string{"example.com"}
		},
		"a wildcard": func(s *vmmdpb.RunSandboxStart) {
			s.NetworkMode, s.EgressDeny = api.SandboxNetworkWeb, []string{"*.example.com"}
		},
		"an address": func(s *vmmdpb.RunSandboxStart) {
			s.NetworkMode, s.EgressAllow = api.SandboxNetworkWeb, []string{"10.0.0.1"}
		},
		"too many": func(s *vmmdpb.RunSandboxStart) {
			s.NetworkMode = api.SandboxNetworkWeb
			for i := range api.SandboxEgressRulesMax + 1 {
				s.EgressAllow = append(s.EgressAllow, "site"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+".com")
			}
		},
	} {
		vmm := newSandboxRunVMM()
		cli := newSandboxRunClient(t, vmm, true)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		stream, err := cli.RunSandbox(ctx)
		if err != nil {
			t.Fatal(err)
		}
		start := sandboxRunStart()
		mutate(start.GetStart())
		_ = stream.Send(start)
		_, err = stream.Recv()
		cancel()
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v, want InvalidArgument", name, err)
		}
	}
}
