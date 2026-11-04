package main

// public_block.go — the operator's block list on a node's public entry
// addresses (ADR-20261103163954 §2). The rendered ruleset declares the sets;
// this adds their elements after every reload, which flushes them, and on a
// loop, so an edit to the file takes effect within
// api.PublicBlockListRefreshInterval.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// publicBlockListPath is the operator's block list: one
// "<prefix> <expiry RFC 3339> [reason]" per line (docs/runbooks/EdgeDirectEntry.md).
const publicBlockListPath = "/etc/faas/public-block-list"

type publicBlockApplier struct {
	path       string
	stagingDir string
	nft        nftExec
	log        *slog.Logger
	now        func() time.Time

	// last is what the previous pass reported, so an unchanged list logs
	// nothing.
	last string
}

// apply adds the live entries to the block sets. The caller holds the
// watcher's lock, so a reload cannot flush the sets halfway through. Errors
// are logged, never returned: the block list must not hold up a reload.
func (a *publicBlockApplier) apply(ctx context.Context) {
	policy := netns.ActiveHostPolicyForRender()
	if policy == nil || len(policy.PublicEntryAddrs) == 0 {
		return
	}
	data, err := os.ReadFile(a.path)
	if errors.Is(err, fs.ErrNotExist) {
		a.report("no block list", nil)
		return
	}
	if err != nil {
		a.report("block list unreadable", err)
		return
	}
	entries, invalid := netns.ParsePublicBlockList(data, a.now())
	for _, err := range invalid {
		a.log.Warn("vmmd: public block list line skipped", "path", a.path, "err", err)
	}
	script := netns.PublicBlockScript(entries, policy.PublicEntryAddrs)
	if script == "" {
		a.report(fmt.Sprintf("block list applied: 0 entries, %d invalid", len(invalid)), nil)
		return
	}
	if err := a.load(ctx, script); err != nil {
		// One by one, so a range that conflicts with another (an
		// overlapping interval) does not hold up the rest.
		var failed []string
		for _, line := range strings.SplitAfter(strings.TrimSuffix(script, "\n"), "\n") {
			if err := a.load(ctx, line); err != nil {
				failed = append(failed, strings.TrimSpace(line)+": "+err.Error())
			}
		}
		if len(failed) > 0 {
			a.report(fmt.Sprintf("block list applied with %d failures", len(failed)), errors.New(strings.Join(failed, "; ")))
			return
		}
	}
	a.report(fmt.Sprintf("block list applied: %d entries, %d invalid", strings.Count(script, "\n"), len(invalid)), nil)
}

// load runs script with nft -f.
func (a *publicBlockApplier) load(ctx context.Context, script string) error {
	if err := os.MkdirAll(a.stagingDir, 0o755); err != nil {
		return fmt.Errorf("create staging directory %s: %w", a.stagingDir, err)
	}
	path := filepath.Join(a.stagingDir, "public-block.nft")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return a.nft.Reload(ctx, path)
}

// report logs a pass whose outcome differs from the previous one.
func (a *publicBlockApplier) report(outcome string, err error) {
	key := outcome
	if err != nil {
		key += ": " + err.Error()
	}
	if key == a.last {
		return
	}
	a.last = key
	if err != nil {
		a.log.Warn("vmmd: public block list", "path", a.path, "outcome", outcome, "err", err)
		return
	}
	a.log.Info("vmmd: public block list", "path", a.path, "outcome", outcome)
}

// run applies the list every api.PublicBlockListRefreshInterval under w's
// lock until ctx ends.
func (a *publicBlockApplier) run(ctx context.Context, w *egressWatcher) {
	ticker := time.NewTicker(api.PublicBlockListRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.mu.Lock()
			a.apply(ctx)
			w.mu.Unlock()
		}
	}
}
