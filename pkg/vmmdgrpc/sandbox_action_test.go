// adr: 20261117065818 §2 — a browser session in web mode announces actions
// at boot; each action runs gregale-browser act with its validated fields
// as flags and its one value after --, and its end comes back on the
// session's stream. A session that takes no actions answers one with an
// error.

package vmmdgrpc_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func browserStart(browser bool) *vmmdpb.RunSandboxRequest {
	r := sandboxRunStart()
	if browser {
		r.GetStart().Browser, r.GetStart().NetworkMode = true, api.SandboxNetworkWeb
	}
	return r
}

func action(id string, mutate func(*vmmdpb.RunSandboxAction)) *vmmdpb.RunSandboxRequest {
	a := &vmmdpb.RunSandboxAction{ActionId: id, Kind: "navigate", Url: "https://example.com/", MaxChars: 4000, TimeoutMs: 30_000,
		MaxOutputBytes: int64(api.SandboxTaskOutputMaxBytes)}
	if mutate != nil {
		mutate(a)
	}
	return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Action{Action: a}}
}

func TestRunSandboxRunsBrowserActions(t *testing.T) {
	vmm := &taskVMM{sandboxRunVMM: newSandboxRunVMM()}
	cli := newSandboxRunClient(t, vmm, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := cli.RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(browserStart(true)); err != nil {
		t.Fatal(err)
	}
	booted := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetBooted() != nil })
	if !booted.GetBooted().GetActions() {
		t.Fatal("a browser session in web mode did not announce actions")
	}
	for _, tc := range []struct {
		id     string
		mutate func(*vmmdpb.RunSandboxAction)
		argv   []string
		err    string
	}{
		{id: "nav", argv: []string{"gregale-browser", "act", "navigate", "--max-chars", "4000", "--timeout", "30", "--", "https://example.com/"}},
		{id: "type", mutate: func(a *vmmdpb.RunSandboxAction) { a.Kind, a.Ref, a.Text, a.Submit = "type", 3, "--rm -rf; $(x)", true },
			argv: []string{"gregale-browser", "act", "type", "--max-chars", "4000", "--timeout", "30", "--ref", "3", "--submit", "--", "--rm -rf; $(x)"}},
		{id: "press", mutate: func(a *vmmdpb.RunSandboxAction) { a.Kind, a.Key = "press", "PageDown" },
			argv: []string{"gregale-browser", "act", "press", "--max-chars", "4000", "--timeout", "30", "--key", "PageDown"}},
		{id: "scroll", mutate: func(a *vmmdpb.RunSandboxAction) { a.Kind, a.Direction = "scroll", "up" },
			argv: []string{"gregale-browser", "act", "scroll", "--max-chars", "4000", "--timeout", "30", "--direction", "up"}},
		{id: "0b6f8c1e-4d2a-4c3b-9f00-1234567890ab", mutate: func(a *vmmdpb.RunSandboxAction) { a.Kind, a.Screenshot = "snapshot", true },
			argv: []string{"gregale-browser", "act", "snapshot", "--max-chars", "4000", "--timeout", "30", "--screenshot",
				"/workspace/.gregale-screenshot-0b6f8c1e-4d2a-4c3b-9f00-1234567890ab.jpg"}},
		{id: "not-a-uuid", mutate: func(a *vmmdpb.RunSandboxAction) { a.Screenshot = true }, err: "UUID"},
		{id: "private", mutate: func(a *vmmdpb.RunSandboxAction) { a.Url = "http://10.0.0.1/" }, err: "url"},
		{id: "no-ref", mutate: func(a *vmmdpb.RunSandboxAction) { a.Kind = "click" }, err: "ref"},
		{id: "key", mutate: func(a *vmmdpb.RunSandboxAction) { a.Kind, a.Key = "press", "F12" }, err: "key"},
		{id: "kind", mutate: func(a *vmmdpb.RunSandboxAction) { a.Kind = "eval" }, err: "kind"},
		{id: "long", mutate: func(a *vmmdpb.RunSandboxAction) {
			a.Kind, a.Ref, a.Text = "type", 1, strings.Repeat("x", api.SandboxBrowserTypeMaxChars+1)
		}, err: "text"},
		{id: "slow", mutate: func(a *vmmdpb.RunSandboxAction) { a.TimeoutMs = 31_000 }, err: "timeout"},
	} {
		vmm.tmu.Lock()
		vmm.argv = nil
		vmm.tmu.Unlock()
		if err := stream.Send(action(tc.id, tc.mutate)); err != nil {
			t.Fatal(err)
		}
		ev := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetActionDone() != nil })
		done := ev.GetActionDone()
		if done.GetActionId() != tc.id {
			t.Fatalf("%s: done for %q", tc.id, done.GetActionId())
		}
		vmm.tmu.Lock()
		argv := vmm.argv
		vmm.tmu.Unlock()
		if tc.err != "" {
			if done.GetExitCode() != -1 || !strings.Contains(done.GetError(), tc.err) || argv != nil {
				t.Fatalf("%s: done = %+v, ran %q", tc.id, done, argv)
			}
			continue
		}
		if done.GetExitCode() != 0 || string(done.GetOutput()) != `{"title":"E"}` || !slices.Equal(argv, tc.argv) {
			t.Fatalf("%s: done = %+v, ran %q", tc.id, done, argv)
		}
	}
}

func TestRunSandboxRefusesActionsOutsideBrowserSessions(t *testing.T) {
	vmm := &taskVMM{sandboxRunVMM: newSandboxRunVMM()}
	cli := newSandboxRunClient(t, vmm, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := cli.RunSandbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(browserStart(false)); err != nil {
		t.Fatal(err)
	}
	if booted := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetBooted() != nil }); booted.GetBooted().GetActions() {
		t.Fatal("a plain session announced actions")
	}
	if err := stream.Send(action("a", nil)); err != nil {
		t.Fatal(err)
	}
	done := recvUntil(t, stream, func(ev *vmmdpb.RunSandboxEvent) bool { return ev.GetActionDone() != nil }).GetActionDone()
	if done.GetExitCode() != -1 || done.GetError() == "" {
		t.Fatalf("done = %+v", done)
	}
	vmm.tmu.Lock()
	defer vmm.tmu.Unlock()
	if vmm.argv != nil {
		t.Fatalf("a plain session ran %q", vmm.argv)
	}
}
