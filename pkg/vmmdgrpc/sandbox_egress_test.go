// adr: 20261117091026 §3 — vmmd takes a session's domain policy only for a
// web-mode session and only as domain rules, puts it on the wake, and says
// at boot that it enforces policies.

package vmmdgrpc_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
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

// sandboxWebLogVMM hands out a queued domain log once.
type sandboxWebLogVMM struct {
	*sandboxRunVMM
	mu      sync.Mutex
	domains []fcvm.SandboxWebDomain
}

func (v *sandboxWebLogVMM) DrainSandboxWebDomains(string) []fcvm.SandboxWebDomain {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := v.domains
	v.domains = nil
	return out
}

// adr: 20261117091026 §4 — vmmd sends a session's domain log on its stream,
// and what is left of it before the stream ends on close.
func TestRunSandboxSendsTheDomainLog(t *testing.T) {
	at := time.UnixMilli(1_763_000_000_000)
	vmm := &sandboxWebLogVMM{sandboxRunVMM: newSandboxRunVMM(), domains: []fcvm.SandboxWebDomain{
		{Domain: "example.com", At: at}, {Domain: "admin.example.com", Refused: fcvm.SandboxWebRefusedPolicy, At: at},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := newSandboxRunClient(t, vmm, true).RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := sandboxRunStart()
	start.GetStart().WebDomainLog = true
	if err := stream.Send(start); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetBooted() != nil })
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}); err != nil {
		t.Fatal(err)
	}
	got := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetWebDomains() != nil }).GetWebDomains().GetDomains()
	if len(got) != 2 || got[0].GetDomain() != "example.com" || got[0].GetRefused() != "" ||
		got[1].GetRefused() != "policy" || got[1].GetAtUnixMs() != at.UnixMilli() {
		t.Fatalf("domains = %v", got)
	}
}

// adr: 20261117091026 §4 — a caller that did not ask for the log never gets
// a frame it may not know.
func TestRunSandboxSendsNoDomainLogUnasked(t *testing.T) {
	vmm := &sandboxWebLogVMM{sandboxRunVMM: newSandboxRunVMM(), domains: []fcvm.SandboxWebDomain{{Domain: "example.com"}}}
	stream := bootSandbox(t, newSandboxRunClient(t, vmm, true))
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}); err != nil {
		t.Fatal(err)
	}
	for {
		ev, err := stream.Recv()
		if err != nil {
			return
		}
		if ev.GetWebDomains() != nil {
			t.Fatalf("unasked domain log %v", ev)
		}
	}
}
