package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// Parallel activation (docs/adr/20261122170000-continuous-unit-lane.md).
// A unit release restarts its daemons in waves instead of one at a time:
// daemons of equal depth in the Lifecycle.After graph have no ordering path
// between them, so each wave restarts together through the same restartOne
// (preload, hand-off or restart, readiness) and the next wave starts only
// when every daemon of this one is ready. A failure in a wave lets the
// rest of that wave finish, then stops: the caller rolls back as before.

// restartParallelEnv carries the cap from the unit lane's activation script.
// An environment variable, not a flag: the script may run a deployctl from
// before this change, which ignores it and restarts one at a time.
const restartParallelEnv = "DEPLOYCTL_RESTART_PARALLEL"

// soloRestart restarts alone, ahead of anything at its depth: vmmd's
// RuntimeDirectory=faas re-creates /run/faas as root:root on start, which
// ensureRunFaasOwnership repairs before the next daemon binds its socket
// there. Restarting another daemon beside it would race that repair.
var soloRestart = map[string]bool{"vmmd": true}

// restartParallelFromEnv reads restartParallelEnv: 0 (one at a time) when
// unset, otherwise a cap from 1 to 16.
func restartParallelFromEnv() (int, error) {
	raw, ok := os.LookupEnv(restartParallelEnv)
	if !ok || raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 16 {
		return 0, fmt.Errorf("%s must be 1..16, got %q", restartParallelEnv, raw)
	}
	return n, nil
}

// restartWaves splits order (already dependency-ordered) into waves: by
// restart depth, solo daemons first and alone, the rest of a depth in
// order and at most limit per wave. A daemon the registry does not know
// gets a wave of its own in its place, as the one-at-a-time loop treated
// it.
func restartWaves(order []string, depth map[string]int, limit int) [][]string {
	if limit < 1 {
		limit = 1
	}
	var waves [][]string
	for i := 0; i < len(order); {
		d, known := depth[order[i]]
		if !known {
			waves = append(waves, []string{order[i]})
			i++
			continue
		}
		// The run of order at this depth: RestartOrder groups a depth
		// together, but an override order may not, so stop at the first
		// daemon of another depth and keep the given order.
		j := i
		for j < len(order) {
			if dj, ok := depth[order[j]]; !ok || dj != d {
				break
			}
			j++
		}
		var rest []string
		for _, service := range order[i:j] {
			if soloRestart[service] {
				waves = append(waves, []string{service})
			} else {
				rest = append(rest, service)
			}
		}
		for len(rest) > 0 {
			n := min(limit, len(rest))
			waves = append(waves, rest[:n])
			rest = rest[n:]
		}
		i = j
	}
	return waves
}

func (r hostRuntime) restartInWaves(ctx context.Context, order []string) error {
	depth, err := daemonunitspec.RestartDepth()
	if err != nil {
		return fmt.Errorf("resolve restart depth: %w", err)
	}
	for _, wave := range restartWaves(order, depth, r.restartParallel) {
		if len(wave) > 1 {
			fmt.Fprintf(os.Stderr, "deployctl: restarting together: %v\n", wave)
		}
		if err := r.restartWave(ctx, wave); err != nil {
			return err
		}
	}
	return nil
}

// restartWave restarts every daemon of wave at once and waits for all of
// them; the errors come back in wave order.
func (r hostRuntime) restartWave(ctx context.Context, wave []string) error {
	if len(wave) == 1 {
		return r.restartOne(ctx, wave[0], true)
	}
	errs := make([]error, len(wave))
	var wg sync.WaitGroup
	for i, service := range wave {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.restartOne(ctx, service, true); err != nil {
				errs[i] = fmt.Errorf("restart %s: %w", service, err)
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
