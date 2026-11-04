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
	"time"

	"github.com/BurntSushi/toml"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
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
type staticEgressIPFile struct {
	Entries []struct {
		AccountID string `toml:"account_id"`
		AppID     string `toml:"app_id"`
		IP        string `toml:"ip"`
	} `toml:"entries"`
}

// LoadStaticEgressIPBundle reads the bundle from path. Missing
// file returns the zero-value bundle (= "no static IPs
// configured"). Per-entry parse errors, reserved-range IPs, and
// malformed rows are Warned and dropped; the rest of the file
// still loads.
//
// The deny set is the canonical api.ValidateStaticEgressIP —
// the same helper apid uses, the same helper pkg/fcvm uses
// at Wake, the same helper used in the metal test. Adding a
// new denied range in one place extends every gate.
func LoadStaticEgressIPBundle(path string, log *slog.Logger) (StaticEgressIPBundle, error) {
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

// writeProvisionedStaticEgressIPs (ADR-119 redesign) mirrors the
// operator's TOML into the Postgres gate table. Called once
// per SIGHUP (and once at startup). The store runs the
// DELETE+INSERT inside a single transaction so the apid PUT
// path sees either the prior set OR the new set, never a
// partial mix.
//
// Errors are logged at Warn and returned — the bridge alias
// + host renderer still get the new entries, so a Postgres
// hiccup degrades to "operator provisioning not synced" (the
// apid PUT returns 404 until a periodic retry succeeds). This
// is the right tradeoff: better to partially lose the gate
// than to block the bundle reload.
//
// Each stable vmmd source replaces its complete set, including accounts
// that disappeared from the file. Other nodes retain their own grants.
func writeProvisionedStaticEgressIPs(ctx context.Context, st state.Store, entries []StaticEgressIPEntry, log *slog.Logger, sources ...string) error {
	source := "default-local"
	if len(sources) > 0 && sources[0] != "" {
		source = sources[0]
	}
	grants := make([]state.StaticEgressIPGrant, 0, len(entries))
	for _, entry := range entries {
		grants = append(grants, state.StaticEgressIPGrant{AccountID: entry.AccountID, IP: entry.IP})
	}
	writer, ok := st.(state.StaticEgressProvisioningStore)
	if !ok {
		return fmt.Errorf("static egress provisioning source storage unavailable")
	}
	publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := writer.ReplaceStaticEgressProvisioning(publishCtx, source, grants); err != nil {
		log.Warn("vmmd: static egress provisioning publication failed; retrying", "source", source, "err", err)
		return err
	}
	return nil
}

// watchStaticEgressIPBundleReload is the SIGHUP-driven reload
// goroutine for the static-IP TOML (ADR-119). On every hupCh
// receive, it re-reads the bundle from path and:
//
//  1. forwards the entries to mgr.SetStaticEgressIPAliases so
//     the bridge alias set on br-tenants stays in sync with
//     the operator file (existing behaviour);
//  2. mirrors the (accountID, customerIP) tuples into the
//     Postgres gate table via
//     writeProvisionedStaticEgressIPs (new in the redesign
//     — the apid PUT path reads Postgres, not the bundle).
//
// Empty path = "no static IP bundle configured" — the goroutine
// skips cleanly (same posture as watchEgressBundleReload above).
// Each watcher receives its own signal registration so one SIGHUP
// reaches both policy reloaders.
//
// Failure model: a missing file is not an error (returns
// zero-value bundle = "remove all aliases"). A malformed file
// keeps the prior alias set live — the reload never silently
// strips a customer's IP because of a parse glitch.
func watchStaticEgressIPBundleReload(ctx context.Context, mgr staticEgressIPTarget, st state.Store, path string, log *slog.Logger, hupCh <-chan os.Signal, sources ...string) {
	if path == "" {
		log.Debug("vmmd: static egress IP bundle reload disabled (no path configured)")
		if st != nil {
			publishStaticEgressBundle(ctx, mgr, st, nil, log, sources...)
		}
		return
	}
	var desired []StaticEgressIPEntry
	loaded := false
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// Startup load: install aliases before any Wake observes
	// the bridge, so a fresh vmmd with a non-empty bundle has
	// the customer IPs aliased before any per-VM SNAT rule
	// fires. A missing file is benign (zero entries = "remove
	// all aliases"); a malformed file is Warned and the prior
	// alias set stays live.
	if bundle, err := LoadStaticEgressIPBundle(path, log); err != nil {
		log.Warn("vmmd: static egress IP bundle startup load failed; running with prior alias set",
			"path", path, "err", err)
	} else {
		mgr.SetStaticEgressIPAliases(bundle.Entries)
		loaded = true
		desired = bundle.Entries
		publishStaticEgressBundle(ctx, mgr, st, desired, log, sources...)
		log.Info("vmmd: static egress IP bundle loaded at startup",
			"path", path, "entries", len(bundle.Entries))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if loaded {
				mgr.SetStaticEgressIPAliases(desired)
				publishStaticEgressBundle(ctx, mgr, st, desired, log, sources...)
			}
		case <-hupCh:
			log.Info("vmmd: SIGHUP received, reloading static egress IP bundle")
			bundle, err := LoadStaticEgressIPBundle(path, log)
			if err != nil {
				log.Warn("vmmd: static egress IP bundle reload failed; keeping prior alias set",
					"path", path, "err", err)
				continue
			}
			mgr.SetStaticEgressIPAliases(bundle.Entries)
			loaded = true
			desired = bundle.Entries
			publishStaticEgressBundle(ctx, mgr, st, desired, log, sources...)
			log.Info("vmmd: static egress IP bundle reloaded",
				"path", path, "entries", len(bundle.Entries))
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

func publishStaticEgressBundle(ctx context.Context, mgr staticEgressIPTarget, st state.Store, entries []StaticEgressIPEntry, log *slog.Logger, sources ...string) {
	_ = writeProvisionedStaticEgressIPs(ctx, st, entries, log, sources...)
	if repair, ok := mgr.(interface{ ReconcileStaticEgressPolicies(context.Context) error }); ok {
		repairCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := repair.ReconcileStaticEgressPolicies(repairCtx); err != nil {
			log.Warn("vmmd: repair static egress after bundle publication", "err", err)
		}
	}
}
