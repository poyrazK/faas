package main

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

func TestEgressWatcherConditionalPolicyFields(t *testing.T) {
	old := *netns.ActiveHostPolicyForRender()
	t.Cleanup(func() { netns.SwapActiveHostPolicy(old) })
	policy := netns.DefaultHostPolicy
	netns.SwapActiveHostPolicy(policy)
	root := t.TempDir()
	w := newEgressWatcher(nil, filepath.Join(root, "staging"), filepath.Join(root, "nftables.conf"))
	nft := &stubNftExec{}
	w.nft = nft
	ctx := context.Background()
	if err := w.RenderIfChanged(ctx); err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		change func(*netns.HostPolicy)
	}{
		{"smtp-add", func(p *netns.HostPolicy) {
			p.SMTPAllowlistRules = []netns.SMTPAllowlistRule{{SourceIP: netip.MustParseAddr("10.100.0.2"), Destinations: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}, AppID: "app"}}
		}},
		{"smtp-source", func(p *netns.HostPolicy) {
			p.SMTPAllowlistRules = append([]netns.SMTPAllowlistRule(nil), p.SMTPAllowlistRules...)
			p.SMTPAllowlistRules[0].SourceIP = netip.MustParseAddr("10.100.0.3")
		}},
		{"smtp-remove", func(p *netns.HostPolicy) { p.SMTPAllowlistRules = nil }},
		{"private-input", func(p *netns.HostPolicy) {
			p.PrivateInputAllowRules = []netns.PrivateInputAllowRule{{Source: netip.MustParsePrefix("10.156.0.0/24"), Ports: []int{9007}}}
		}},
		{"public-interface", func(p *netns.HostPolicy) { p.PublicIface = "ens5" }},
	}
	for i, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutation.change(&policy)
			netns.SwapActiveHostPolicy(policy)
			if err := w.RenderIfChanged(ctx); err != nil {
				t.Fatal(err)
			}
			if len(nft.loadCalls) != i+2 {
				t.Fatal("changed full policy was skipped")
			}
			if err := w.RenderIfChanged(ctx); err != nil {
				t.Fatal(err)
			}
			if len(nft.loadCalls) != i+2 {
				t.Fatal("unchanged policy reapplied")
			}
		})
	}
}

func TestEgressWatcherConditional(t *testing.T) {
	nft := &stubNftExec{}
	w, _, live := newTestWatcher(t, nft)
	body := "policy A"
	w.render = func() string { return body }
	ctx := context.Background()
	check := func(want int, call func(context.Context) error) {
		t.Helper()
		if err := call(ctx); err != nil {
			t.Fatal(err)
		}
		if len(nft.loadCalls) != want || len(nft.checkCalls) != want {
			t.Fatalf("checks/loads = %d/%d, want %d", len(nft.checkCalls), len(nft.loadCalls), want)
		}
	}
	// A pre-existing file must not be mistaken for a successfully applied policy.
	if err := os.WriteFile(live, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	check(1, w.RenderIfChanged)
	check(1, w.RenderIfChanged)
	body = "policy B"
	check(2, w.RenderIfChanged)
	check(2, w.RenderIfChanged)
	check(3, w.Reload) // Operator notifications always repair the kernel.
	check(4, w.Render) // Teardown/drift retains forced behavior.
	w2 := &egressWatcher{nft: nft, render: w.render, stagingDir: w.stagingDir, livePath: live}
	check(5, w2.RenderIfChanged) // Restart cannot inherit an unverified cache.
}

func TestEgressWatcherConditionalFailureInvalidates(t *testing.T) {
	for _, stage := range []string{"mkdir", "write", "syntax", "replace", "load"} {
		t.Run(stage, func(t *testing.T) {
			nft := &stubNftExec{}
			w, dir, live := newTestWatcher(t, nft)
			body := "policy A"
			w.render = func() string { return body }
			ctx := context.Background()
			if err := w.RenderIfChanged(ctx); err != nil {
				t.Fatal(err)
			}
			body = "policy B"
			repair := func() {}
			switch stage {
			case "mkdir":
				w.stagingDir = live // Existing regular file cannot be a directory.
				repair = func() { w.stagingDir = dir }
			case "write":
				path := filepath.Join(dir, "nftables.conf.staging")
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
				repair = func() {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			case "syntax":
				nft.checkErr = errors.New("invalid policy")
				repair = func() { nft.checkErr = nil }
			case "replace":
				w.livePath = filepath.Join(live, "not-a-directory")
				repair = func() { w.livePath = live }
			case "load":
				nft.loadErr = errors.New("kernel reload failed")
				repair = func() { nft.loadErr = nil }
			}
			if err := w.RenderIfChanged(ctx); err == nil {
				t.Fatal("failure was swallowed")
			}
			if w.hasApplied {
				t.Fatal("failed update retained trusted cache")
			}
			repair()
			before := len(nft.loadCalls)
			body = "policy A" // Reverting to the OLD cached body must still repair.
			if err := w.RenderIfChanged(ctx); err != nil {
				t.Fatal(err)
			}
			if len(nft.loadCalls) != before+1 {
				t.Fatal("retry incorrectly skipped")
			}
			if err := w.RenderIfChanged(ctx); err != nil {
				t.Fatal(err)
			}
			if len(nft.loadCalls) != before+1 {
				t.Fatal("successful retry not cached")
			}
		})
	}
}

func TestEgressWatcherConditionalFailedForcedRepair(t *testing.T) {
	nft := &stubNftExec{}
	w, _, _ := newTestWatcher(t, nft)
	ctx := context.Background()
	if err := w.RenderIfChanged(ctx); err != nil {
		t.Fatal(err)
	}
	nft.loadErr = errors.New("failed repair")
	if err := w.Reload(ctx); err == nil {
		t.Fatal("expected forced repair failure")
	}
	nft.loadErr = nil
	if err := w.RenderIfChanged(ctx); err != nil {
		t.Fatal(err)
	}
	if len(nft.loadCalls) != 3 {
		t.Fatalf("loads=%d, want 3", len(nft.loadCalls))
	}
}

func TestEgressWatcherConditionalConcurrent(t *testing.T) {
	nft := &stubNftExec{}
	w, _, _ := newTestWatcher(t, nft)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			if err := w.RenderIfChanged(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(nft.loadCalls) != 1 {
		t.Fatalf("loads=%d, want 1", len(nft.loadCalls))
	}
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if err := w.Reload(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(nft.loadCalls) != 17 {
		t.Fatalf("loads=%d, want 17", len(nft.loadCalls))
	}
}

func TestEgressWatcherConditionalCanceled(t *testing.T) {
	nft := &stubNftExec{}
	w, _, _ := newTestWatcher(t, nft)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.RenderIfChanged(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(nft.checkCalls) != 0 {
		t.Fatal("canceled reload touched nft")
	}
	if err := w.RenderIfChanged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.Reload(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if err := w.RenderIfChanged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(nft.loadCalls) != 2 {
		t.Fatal("canceled forced repair retained cache")
	}
}
