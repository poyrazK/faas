package main

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

// scriptNft records the scripts the block list loads and fails any script
// containing failOn.
type scriptNft struct {
	mu      sync.Mutex
	failOn  string
	scripts []string
}

func (s *scriptNft) CheckSyntax(context.Context, string) error { return nil }

func (s *scriptNft) Reload(_ context.Context, path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts = append(s.scripts, string(body))
	if s.failOn != "" && strings.Contains(string(body), s.failOn) {
		return errors.New("conflicting intervals")
	}
	return nil
}

// withEntryPolicy makes the active host policy carry entry addrs for the test.
func withEntryPolicy(t *testing.T, addrs ...netip.Addr) {
	t.Helper()
	prev := netns.ActiveHostPolicyForRender()
	next := *prev
	next.PublicEntryAddrs = addrs
	netns.SwapActiveHostPolicy(next)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(*prev) })
}

func newTestBlockApplier(t *testing.T, nft nftExec, list string) *publicBlockApplier {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "public-block-list")
	if list != "" {
		if err := os.WriteFile(path, []byte(list), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	return &publicBlockApplier{path: path, stagingDir: filepath.Join(dir, "staging"), nft: nft, log: newDiscardLogger(), now: func() time.Time { return now }}
}

// A reload flushes the ruleset, so the watcher re-adds the block list right
// after it, under the same lock (ADR-20261103163954 §2).
func TestPublicBlockListIsReaddedAfterEveryReload(t *testing.T) {
	withEntryPolicy(t, netip.MustParseAddr("203.0.113.10"))
	nft := &scriptNft{}
	w, _, _ := newTestWatcher(t, nft)
	blocks := newTestBlockApplier(t, nft, "192.0.2.0/24 2026-11-04T13:00:00Z flood\n")
	w.afterReload = blocks.apply
	for range 2 {
		if err := w.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var adds int
	for _, s := range nft.scripts {
		if s == "add element inet faas public_block_v4 { 192.0.2.0/24 timeout 3600s }\n" {
			adds++
		}
	}
	if adds != 2 {
		t.Fatalf("block list loaded %d times over two reloads, want 2; scripts: %q", adds, nft.scripts)
	}
}

// One range that nft refuses does not hold up the others.
func TestPublicBlockListAppliesEachEntryWhenTheBatchFails(t *testing.T) {
	withEntryPolicy(t, netip.MustParseAddr("203.0.113.10"))
	nft := &scriptNft{failOn: "198.51.100.0/24"}
	blocks := newTestBlockApplier(t, nft, "192.0.2.0/24 2026-11-04T13:00:00Z\n198.51.100.0/24 2026-11-04T13:00:00Z\n203.0.113.0/24 2026-11-04T13:00:00Z\n")
	blocks.apply(t.Context())
	if len(nft.scripts) != 4 {
		t.Fatalf("loads = %d, want the batch then each of 3 entries: %q", len(nft.scripts), nft.scripts)
	}
	if !strings.Contains(blocks.last, "1 failures") {
		t.Fatalf("outcome = %q, want one failure reported", blocks.last)
	}
}

// Nothing is loaded on a node with no entry address, or with no list.
func TestPublicBlockListNeedsEntryAddressesAndAList(t *testing.T) {
	nft := &scriptNft{}
	newTestBlockApplier(t, nft, "192.0.2.0/24 2026-11-04T13:00:00Z\n").apply(t.Context())
	withEntryPolicy(t, netip.MustParseAddr("203.0.113.10"))
	newTestBlockApplier(t, nft, "").apply(t.Context())
	if len(nft.scripts) != 0 {
		t.Fatalf("loaded %q, want nothing", nft.scripts)
	}
}
