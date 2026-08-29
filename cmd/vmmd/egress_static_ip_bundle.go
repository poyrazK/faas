// Package main contains vmmd's runtime helpers (cmd/vmmd/main.go
// is the daemon entry point; the auxiliary helpers live alongside).
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
)

// StaticEgressIPBundle (ADR-119 redesign) is the operator-supplied
// static egress IP set vmmd aliases onto br-tenants at startup AND
// on SIGHUP-reload. Each entry is an (accountID, appID, ip) tuple
// the operator has pre-provisioned on the host's AS (Hetzner
// additional IP, AWS EIP, etc.); the renderer in
// pkg/netns/policy.go emits one host-side SNAT rule per live VM
// mapping the customer's egress to the matching IP.
//
// The bundle is sorted + dedup'd at load time. Reserved-range
// entries (RFC1918, link-local, multicast, loopback, CGN) are
// rejected at load with a Warn — the canonical deny set
// pkg/api.ValidateStaticEgressIP is the single source of truth
// for "legal customer IP". IPv6 entries are also rejected (v6
// deferred per ADR-119 §3).
//
// The file is a flat list keyed by (app_id, ip). Multiple
// entries for the same app are collapsed with the last one
// winning; the wire path on schedd's side re-validates per-app
// quota so a malformed entry can't bypass it.
type StaticEgressIPBundle struct {
	// Entries is sorted (by .AppID) and dedup'd (by app_id; last
	// IP for a given app wins). Reserved / invalid entries are
	// excluded.
	Entries []StaticEgressIPEntry
}

// StaticEgressIPEntry is one (accountID, appID, ip) tuple from
// the TOML. The type is aliased to pkg/fcvm.StaticEgressIPEntry
// so the Manager's bundle-replay path (which reads the on-disk
// state at startup) consumes the same shape. The AccountID is
// what feeds the Postgres gate table (provisioned_static_egress_ips)
// on SIGHUP reload.
type StaticEgressIPEntry = fcvm.StaticEgressIPEntry

// staticEgressIPFile is the on-disk TOML shape. Same flat-list
// shape as the operator-allowlist bundle so the loader stays
// trivial. account_id is the apps.account_id the entry is
// provisioned for (the Postgres gate table is keyed on
// (account_id, customer_ip)); app_id is the apps.id of the
// pinned app; ip is the customer-supplied v4 address.
//
// ADR-119 v2 — node is the compute_nodes.name the IP is
// provisioned on (the operator's authoritative view). Each
// vmmd only handles entries where node == this vmmd's own
// nodeID; entries with a different node are filtered out at
// load time. An empty node field = "use this vmmd's own node"
// (legacy single-box default; matches the v1 wire shape where
// there was only one box).
type staticEgressIPFile struct {
	Entries []struct {
		AccountID string `toml:"account_id"`
		AppID     string `toml:"app_id"`
		IP        string `toml:"ip"`
		Node      string `toml:"node"`
	} `toml:"entries"`
}

// LoadStaticEgressIPBundle reads the bundle from path and
// returns the per-(own-node) slice. Missing file returns the
// zero-value bundle (= "no static IPs configured"). Per-entry
// parse errors, reserved-range IPs, malformed rows, and
// cross-node entries (entries whose `node` field != ownNodeName)
// are Warned and dropped; the rest of the file still loads.
//
// ADR-119 v2 — ownNodeName is this vmmd's resolved
// compute_nodes.name (see resolveOwnNode in cmd/vmmd/main.go).
// Each vmmd is only responsible for entries where:
//   - the entry's `node` field is empty (legacy single-box
//     default; matches the pre-v2 wire shape)
//   - OR the entry's `node` field equals ownNodeName
//
// All other entries are dropped at load (the Postgres gate write
// would be a routing bug — same nodeID semantics as the
// UpdateStaticEgressIP gRPC handler's FailedPrecondition check).
// When ownNodeName is empty, every entry passes the filter
// (the legacy single-box install: no compute_nodes row, no
// per-node partition).
//
// The deny set is the canonical api.ValidateStaticEgressIP —
// the same helper apid uses, the same helper pkg/fcvm uses
// at Wake, the same helper used in the metal test. Adding a
// new denied range in one place extends every gate.
func LoadStaticEgressIPBundle(path string, ownNodeName string, log *slog.Logger) (StaticEgressIPBundle, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return StaticEgressIPBundle{}, nil
		}
		return StaticEgressIPBundle{}, fmt.Errorf("vmmd: static egress IP bundle: read %q: %w", path, err)
	}
	if len(b) == 0 {
		return StaticEgressIPBundle{}, nil
	}
	var raw staticEgressIPFile
	if err := toml.Unmarshal(b, &raw); err != nil {
		return StaticEgressIPBundle{}, fmt.Errorf("vmmd: static egress IP bundle: parse %q: %w", path, err)
	}
	type seenEntry struct {
		AccountID string
		IP        netip.Addr
	}
	seen := make(map[string]seenEntry, len(raw.Entries))
	out := make([]StaticEgressIPEntry, 0, len(raw.Entries))
	for _, e := range raw.Entries {
		accountID := strings.TrimSpace(e.AccountID)
		appID := strings.TrimSpace(e.AppID)
		ipStr := strings.TrimSpace(e.IP)
		nodeName := strings.TrimSpace(e.Node)
		if accountID == "" {
			log.Warn("vmmd: static egress IP bundle: dropping entry with empty account_id",
				"path", path, "app_id", appID, "ip", ipStr)
			continue
		}
		if appID == "" {
			log.Warn("vmmd: static egress IP bundle: dropping entry with empty app_id",
				"path", path, "account_id", accountID, "ip", ipStr)
			continue
		}
		if ipStr == "" {
			log.Warn("vmmd: static egress IP bundle: dropping entry with empty ip",
				"path", path, "account_id", accountID, "app_id", appID)
			continue
		}
		// Per-node filter (ADR-119 v2). Empty node field =
		// legacy default ("this vmmd's own node"). A non-empty
		// field that doesn't match ownNodeName is dropped
		// silently with a Debug log — this is the operator's
		// expected behaviour for entries provisioned on a
		// different node in a multi-host cluster. When
		// ownNodeName is empty (no compute_nodes row), the
		// filter is a pass-through (legacy single-box).
		if nodeName != "" && ownNodeName != "" && nodeName != ownNodeName {
			log.Debug("vmmd: static egress IP bundle: dropping cross-node entry",
				"path", path, "app_id", appID, "node", nodeName, "own_node", ownNodeName)
			continue
		}
		ip, perr := netip.ParseAddr(ipStr)
		if perr != nil {
			log.Warn("vmmd: static egress IP bundle: dropping invalid IP",
				"path", path, "account_id", accountID, "app_id", appID, "ip", ipStr, "err", perr)
			continue
		}
		if err := api.ValidateStaticEgressIP(ip); err != nil {
			log.Warn("vmmd: static egress IP bundle: dropping IP rejected by canonical deny-set",
				"path", path, "account_id", accountID, "app_id", appID, "ip", ipStr, "err", err)
			continue
		}
		// Last-wins per appID. The pkg/api/limits.go per-app
		// quota of 1 (Scale plan in v1) is enforced upstream
		// by the apid handler; the TOML is the operator-side
		// mirror that runs once at startup.
		seen[appID] = seenEntry{AccountID: accountID, IP: ip}
	}
	for appID, e := range seen {
		out = append(out, StaticEgressIPEntry{AccountID: e.AccountID, AppID: appID, IP: e.IP})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	if len(out) == 0 && len(raw.Entries) > 0 {
		log.Warn("vmmd: static egress IP bundle: empty after filtering (all entries rejected)",
			"path", path)
	}
	return StaticEgressIPBundle{Entries: out}, nil
}

// writeProvisionedStaticEgressIPs (ADR-119 redesign + v2) mirrors
// the operator's TOML into the Postgres gate table. Called once
// per SIGHUP (and once at startup). The store runs the
// DELETE+INSERT inside a single transaction so the apid PUT
// path sees either the prior set OR the new set, never a
// partial mix.
//
// v2 adds the nodeID parameter: each vmmd writes only the IPs
// owned by its own node. The bundle is grouped by accountID
// before the call so each account's set is replaced in one
// transaction (the vmmd is unlikely to have many accounts in
// v1, but the per-account grouping is the right shape for
// multi-account clusters in the future).
//
// Errors are logged at Warn and swallowed — the bridge alias
// + host renderer still get the new entries, so a Postgres
// hiccup degrades to "operator provisioning not synced" (the
// apid PUT returns 404 until the next SIGHUP succeeds). This
// is the right tradeoff: better to partially lose the gate
// than to block the bundle reload.
//
// Empty nodeID is the "legacy single-box pre-compute_nodes"
// path: the caller (cmd/vmmd/main.go) resolves default-local
// from the store before invoking this function. Truly bare
// installs without default-local in compute_nodes have the
// gate write skipped (Warn + continue) — the bridge alias +
// host renderer still get the entries, so the egress works
// for the live VMs; the apid PUT path returns 404 until the
// operator seeds compute_nodes via a future re-up.
func writeProvisionedStaticEgressIPs(ctx context.Context, st state.Store, entries []StaticEgressIPEntry, nodeID string, log *slog.Logger) {
	if nodeID == "" {
		log.Warn("vmmd: static egress IP bundle: skipping Postgres gate write (no node_id resolved — bare install without default-local in compute_nodes)",
			"entries", len(entries))
		return
	}
	byAccount := make(map[string][]netip.Addr)
	for _, e := range entries {
		if e.AccountID == "" || !e.IP.IsValid() {
			continue
		}
		byAccount[e.AccountID] = append(byAccount[e.AccountID], e.IP)
	}
	for accountID, ips := range byAccount {
		if err := st.ReplaceProvisionedStaticEgressIPs(ctx, accountID, nodeID, ips); err != nil {
			log.Warn("vmmd: static egress IP bundle: Postgres gate write failed; apid PUT will 404 until next reload",
				"account_id", accountID, "node_id", nodeID, "ips", len(ips), "err", err)
		}
	}
}

// pushStaticEgressIPRulesToHostRenderer (ADR-119 redesign) is
// the seam between the vmmd bundle and the host renderer. The
// bundle's (PerVMHostIP, CustomerIP) tuples flow into the
// netns.ActiveHostPolicy.StaticEgressRules list, which is the
// canonical input the host renderer reads.
//
// At bundle-reload time we do NOT have per-VM host IPs yet
// (those are allocated at Wake via
// pkg/fcvm.AcquireStaticEgressIP). The bundle carries only
// the (accountID, appID, customerIP) tuple. The per-VM host
// IP is filled in by the Manager.RegisterStaticEgressIPForVM
// call at Wake time, which rebuilds the host renderer.
//
// What we emit here is the "customer IP set" — the renderer
// uses per-VM host IPs as the `ip saddr` source. The two
// maps must compose at Render time (see the renderer's
// rebuildHostStaticEgressRules walk).
//
// For the v1 single-node flow this is sufficient: the apid
// PUT path reserves the per-VM host IP FIRST, then the
// customer's app wakes, then the host renderer rebuilds with
// the (perVMHostIP, customerIP) tuple. The bundle reload
// simply asserts the customer IP set is what we expect.
func pushStaticEgressIPRulesToHostRenderer(entries []StaticEgressIPEntry) {
	cur := netns.ActiveHostPolicyForRender()
	if cur == nil {
		return
	}
	rules := make([]netns.StaticEgressRule, 0, len(entries))
	for _, e := range entries {
		if e.AccountID == "" || e.AppID == "" || !e.IP.IsValid() {
			continue
		}
		rules = append(rules, netns.StaticEgressRule{
			CustomerIP: e.IP,
			AccountID:  e.AccountID,
			AppID:      e.AppID,
		})
	}
	// Copy the current policy, install the new rules, and swap
	// (single atomic pointer copy). The watcher reads the new
	// pointer on the next Render cycle.
	next := *cur
	next.StaticEgressRules = rules
	netns.SwapActiveHostPolicy(next)
}

// watchStaticEgressIPBundleReload is the SIGHUP-driven reload
// goroutine for the static-IP TOML (ADR-119). On every hupCh
// receive, it re-reads the bundle from path and:
//
//  1. forwards the entries to mgr.SetStaticEgressIPAliases so
//     the bridge alias set on br-tenants stays in sync with
//     the operator file (existing behaviour);
//  2. pushes the entries into the host renderer via
//     pushStaticEgressIPRulesToHostRenderer (new in the
//     redesign — the renderer reads ActiveHostPolicy, not
//     DefaultHostPolicy);
//  3. mirrors the (accountID, customerIP) tuples into the
//     Postgres gate table via
//     writeProvisionedStaticEgressIPs (new in the redesign
//     — the apid PUT path reads Postgres, not the bundle).
//
// Empty path = "no static IP bundle configured" — the goroutine
// skips cleanly (same posture as watchEgressBundleReload above).
// The same SIGHUP signal drives both watchers in production;
// vmmd main wires hupCh once and shares it across both.
//
// ADR-119 v2 — ownNodeName is this vmmd's compute_nodes.name.
// LoadStaticEgressIPBundle filters out entries whose `node`
// field != ownNodeName (so each vmmd only handles its own
// slice — see LoadStaticEgressIPBundle's filter docstring).
//
// Failure model: a missing file is not an error (returns
// zero-value bundle = "remove all aliases"). A malformed file
// keeps the prior alias set live — the reload never silently
// strips a customer's IP because of a parse glitch.
func watchStaticEgressIPBundleReload(ctx context.Context, mgr staticEgressIPTarget, st state.Store, path string, ownNodeName, nodeID string, log *slog.Logger, hupCh <-chan os.Signal) {
	if path == "" {
		log.Debug("vmmd: static egress IP bundle reload disabled (no path configured)")
		return
	}
	// Startup load: install aliases before any Wake observes
	// the bridge, so a fresh vmmd with a non-empty bundle has
	// the customer IPs aliased before any per-VM SNAT rule
	// fires. A missing file is benign (zero entries = "remove
	// all aliases"); a malformed file is Warned and the prior
	// alias set stays live.
	if bundle, err := LoadStaticEgressIPBundle(path, ownNodeName, log); err != nil {
		log.Warn("vmmd: static egress IP bundle startup load failed; running with prior alias set",
			"path", path, "err", err)
	} else {
		mgr.SetStaticEgressIPAliases(bundle.Entries)
		pushStaticEgressIPRulesToHostRenderer(bundle.Entries)
		writeProvisionedStaticEgressIPs(ctx, st, bundle.Entries, nodeID, log)
		log.Info("vmmd: static egress IP bundle loaded at startup",
			"path", path, "node_id", nodeID, "own_node", ownNodeName, "entries", len(bundle.Entries))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-hupCh:
			log.Info("vmmd: SIGHUP received, reloading static egress IP bundle")
			bundle, err := LoadStaticEgressIPBundle(path, ownNodeName, log)
			if err != nil {
				log.Warn("vmmd: static egress IP bundle reload failed; keeping prior alias set",
					"path", path, "err", err)
				continue
			}
			mgr.SetStaticEgressIPAliases(bundle.Entries)
			pushStaticEgressIPRulesToHostRenderer(bundle.Entries)
			writeProvisionedStaticEgressIPs(ctx, st, bundle.Entries, nodeID, log)
			log.Info("vmmd: static egress IP bundle reloaded",
				"path", path, "node_id", nodeID, "own_node", ownNodeName, "entries", len(bundle.Entries))
		}
	}
}

// staticEgressIPTarget is the narrow surface the SIGHUP reload
// goroutine needs from *fcvm.Manager. Defined as an interface so
// tests can stub the Manager without booting a real fcvm.Manager
// (same posture as egressBundleTarget above).
type staticEgressIPTarget interface {
	SetStaticEgressIPAliases(entries []StaticEgressIPEntry)
}
