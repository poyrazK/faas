// vmmd self-registration (issue #98 / ADR-028).
//
// On startup, every vmmd Upserts its own row into compute_nodes so
// schedd's placement engine can route wakes to it without an
// operator-driven POST /v1/compute-nodes. UPSERT semantics (rather
// than plain INSERT) means a rebooting box comes back with the same
// UUID + created_at as before — schedd caches the id in memory and
// loses no state across the restart. ON CONFLICT re-applies the
// operator's resource numbers from vmmd.toml AND re-activates a
// previously drained row (active=true), so a fix-up after a network
// blip doesn't need an admin click.
//
// The full node registration happens before the gRPC listener binds:
// if the upsert fails (Postgres down, schema drift), vmmd exits
// rather than serving traffic with no identity. That fail-closed
// stance matches the host-key load above it (the daemon refuses to
// start without its unseal key for the same reason).
//
// The `default-local` row seeded by migration 00024 has the same
// name the vmmd uses when [compute_node].name is left at its
// default — short hostname — only when hostname equals
// "default-local" (rare; tests/legacy). Production operators set
// [compute_node].name explicitly to avoid colliding with the seed
// row, and the vmmd config default-empts that collision by leaving
// NodeName empty when no override is set (skip self-registration
// entirely; schedd only knows this node via default-local).

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// registerComputeNode wires the daemon's startup upsert. Returns
// the registered node on success (which the caller logs and uses to
// confirm the placement engine sees the box) or an error that the
// caller surfaces as a fatal startup failure.
//
// detectOverlayIP is function-typed so tests can inject a stub that
// returns "100.64.0.1" without shelling out to `tailscale ip -4`
// (Linux/macOS only — gated to //go:build metal otherwise).
func registerComputeNode(ctx context.Context, st state.Store, cfg ComputeNodeConfig, listenTarget string, detectOverlayIP func(context.Context) (string, error), log *slog.Logger) (state.ComputeNode, error) {
	name := strings.TrimSpace(cfg.NodeName)
	if name == "" {
		// Empty name = operator chose not to self-register. vmmd
		// still serves traffic; schedd only routes via default-local
		// (migration 00024) so the gateway's per-node client cache
		// only ever holds that one entry. This is the dev-mode
		// path; multi-node boxes must set [compute_node].name.
		log.Info("vmmd: skipping self-registration ([compute_node].name empty); default-local only")
		return state.ComputeNode{}, nil
	}

	if cfg.VPCPUs <= 0 || cfg.MemMB <= 0 || cfg.MaxConcurrency <= 0 || cfg.AdmissionCeilingMB <= 0 {
		return state.ComputeNode{}, fmt.Errorf("vmmd: [compute_node] fields must be > 0 (got vpcpus=%d mem_mb=%d max_concurrency=%d admission_ceiling_mb=%d)",
			cfg.VPCPUs, cfg.MemMB, cfg.MaxConcurrency, cfg.AdmissionCeilingMB)
	}

	overlayIP := strings.TrimSpace(cfg.OverlayIP)
	if overlayIP == "" && detectOverlayIP != nil {
		ip, err := detectOverlayIP(ctx)
		if err != nil {
			// Best-effort: an empty overlay_ip is fine for
			// default-local (which dials over the unix socket,
			// never over an overlay IP). We log and continue
			// rather than fail-closed because remote-node
			// routing requires overlay_ip, but a missing
			// tailscale binary in a dev box is not a vmmd
			// startup error.
			log.Warn("vmmd: overlay IP detection failed; continuing", "err", err.Error())
		} else {
			overlayIP = ip
		}
	}

	row := state.ComputeNode{
		Name:               name,
		TargetURL:          listenTarget,
		VPCPUs:             cfg.VPCPUs,
		MemMB:              cfg.MemMB,
		MaxConcurrency:     cfg.MaxConcurrency,
		AdmissionCeilingMB: cfg.AdmissionCeilingMB,
		Active:             true,
	}
	got, err := st.UpsertComputeNode(ctx, row)
	if err != nil {
		return state.ComputeNode{}, fmt.Errorf("vmmd: upsert compute_nodes %q: %w", name, err)
	}
	log.Info("vmmd: compute_node registered",
		"name", got.Name, "id", got.ID,
		"target_url", got.TargetURL,
		"vpcpus", got.VPCPUs, "mem_mb", got.MemMB,
		"admission_ceiling_mb", got.AdmissionCeilingMB)
	_ = overlayIP // reserved: pkg/state.ComputeNode will get OverlayIP in the migration-00026 follow-up.
	return got, nil
}

// defaultDetectOverlayIP runs `tailscale ip -4` if tailscale is on
// $PATH, returning the first IPv4 it emits. Returns ("", nil) when
// tailscale isn't installed (single-box dev) or returns an empty
// string (WireGuard-mode operators set [compute_node].overlay_ip
// explicitly). Returns ("", err) on an actual exec failure that
// isn't "binary missing" — e.g. tailscale installed but the daemon
// is down — so the caller can log the failure rather than silently
// proceeding.
func defaultDetectOverlayIP(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("tailscale"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	cmd := exec.CommandContext(ctx, "tailscale", "ip", "-4")
	cmd.Env = append(os.Environ(), "TS_NO_LOGS_NO_SUPPORT=true")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tailscale ip -4: %w", err)
	}
	ip := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if ip == "" {
		return "", errors.New("tailscale ip -4 returned empty")
	}
	return ip, nil
}
