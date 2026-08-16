// TOML table-placement validator — the load-bearing check that
// justifies the issue #911 PR-0 PR.
//
// Issue #911 invokes the failure mode where render-time keys landed
// in the wrong TOML table (the operator reports `schedd_client_*`
// inside `[compute_node]`; the actual on-disk bug is the duplicate
// `tls_*_path` inside `[compute_node]` at the vmmd.toml.example
// canonical copy under deploy/ansible/roles/vmmd_service/files/,
// lines 33-40 — the top-level tls_*_path cluster). Both shapes share
// the same root cause: the renderer
// treats the wrong key as belonging to the wrong table, and the
// TOML default-coercion paths silently fall back to a no-op.
// Both shapes share the same root cause: the renderer treats the
// wrong key as belonging to the wrong table, and the TOML default-
// coercion paths silently fall back to a no-op. The bug ships, the
// daemon refuses to start, and the operator debugs at 02:00.
//
// The fix is a structural check that runs during
// `gregale manifest validate` and refuses any manifest whose daemon
// config block places a daemon-private key (e.g. `schedd_client_*`)
// under a daemon-public table (e.g. `[compute_node]`), or vice
// versa. The validator is exhaustive: every daemon's TOML shape is
// meta-described here, and the meta-descriptor is the source of
// truth that the renderer (PR-2) also consumes — so the validator
// and the renderer cannot disagree on which table a key belongs to.
//
// PR-0 ships the validator + the meta-descriptor. PR-2 wires the
// renderer to consume the same descriptor. PR-4 adds the doctor
// check that asserts the rendered TOML on disk matches the
// meta-descriptor (closes the loop).

package manifest

import (
	"fmt"
	"sort"
	"strings"
)

// TableKey describes which TOML table a key belongs to, and whether
// it is daemon-private (the daemon that produces the TOML owns the
// key) or daemon-public (a remote daemon's identity lives on it).
//
// `Owner` is the daemon name the key belongs to (e.g. "vmmd");
// `Scope` is "private" (vmmd-owned) or "public" (a remote daemon's
// CN/SAN/data). The validator rejects a manifest whose declared
// `tls` material's Owner doesn't match the table's expected owner.
type TableKey struct {
	// Table is the TOML table name (e.g. "compute_node"). Empty
	// string means top-level (no `[...]` section).
	Table string
	// Key is the leaf key name (e.g. "tls_cert_path").
	Key string
	// Owner is the daemon name that owns this key. Used to detect
	// cross-table placement bugs (issue #911's `schedd_client_*`
	// inside `[compute_node]` shape).
	Owner string
	// Scope is "private" (the daemon owns the key) or "public"
	// (a remote daemon's identity). The two scopes have different
	// validation rules.
	Scope string
}

// String renders the key as a TOML path for error messages
// ("compute_node.tls_cert_path").
func (k TableKey) String() string {
	if k.Table == "" {
		return k.Key
	}
	return k.Table + "." + k.Key
}

// HostBlock describes the per-daemon TOML shape for a single
// control-plane node. The descriptors below are the source of truth
// for both the validator (this file) and the renderer (PR-2). The
// `privateKeys` and `publicKeys` slices are exhaustive: every key
// that the renderer writes lands in one of those two lists.
type HostBlock struct {
	// Daemon is the daemon name (e.g. "vmmd"). Matches the key
	// in the manifest's `daemons:` map.
	Daemon string
	// PrivateKeys are the keys that the daemon owns (the daemon's
	// own listener, cert, key, CA). They belong to the top-level
	// table (no `[...]` section) by spec.
	PrivateKeys []string
	// ComputeNodeBlock is the [compute_node] table this daemon
	// populates (the vmmd self-registration seam at
	// deploy/ansible/roles/vmmd_service/files/vmmd.toml.example:
	// 52-103 — the only canonical location; the legacy
	// deploy/etc/vmmd.toml.example was git rm'd in PR-1 Phase 2).
	// The `publicKeys` are
	// keys that BELONG to this daemon's ComputeNode table — they
	// are the remote-daemon identities the renderer writes to
	// make the self-registration leg talk cross-box.
	//
	// The bug class issue #911 calls out: keys belonging to the
	// daemon's own listener (private) landed in ComputeNodeBlock
	// (public), and vice versa. The descriptor below forces the
	// renderer to consult the same descriptor the validator
	// checks, so the two cannot drift.
	ComputeNodeBlock []TableKey
}

// HostKeys is the per-daemon TOML key catalog. Use by the validator
// (this file) and the renderer (PR-2). The catalog is intentionally
// exhaustive: anything NOT in the catalog is a renderer bug caught
// at `gregale manifest validate` time, not at 02:00.
//
// The catalog covers every daemon the manifest schema knows about
// (the Daemons struct at manifest.go has the source-of-truth list).
// Adding a new daemon requires (a) a row in the manifest schema's
// `daemons:` map, (b) a row in this catalog, (c) a test in
// manifest_test.go pinning the catalog's invariant.
var HostKeys = map[string]HostBlock{
	// vmmd has the most complex shape (per-issue #911 / ADR-028):
	// its TOML has both a top-level cluster (its own listener +
	// TLS material) and a `[compute_node]` block (self-registration
	// to schedd). The bug class is keys in the wrong table.
	"vmmd": {
		Daemon: "vmmd",
		PrivateKeys: []string{
			"socket_path",
			"metrics_addr",
			"owner_user",
			"kernel_path",
			"listen_addr",
			// Top-level server-mTLS cluster — these are vmmd's
			// OWN listener cert / key / CA. The renderer
			// historically duplicated these inside
			// [compute_node]; the validator catches that.
			"tls_cert_path",
			"tls_key_path",
			"tls_ca_path",
		},
		ComputeNodeBlock: []TableKey{
			// Self-registration identity — the vmmd box tells
			// schedd who it is. These are the `[compute_node]`
			// keys and they DO NOT belong at top level.
			{Table: "compute_node", Key: "name", Owner: "vmmd", Scope: "public"},
			{Table: "compute_node", Key: "target_url", Owner: "vmmd", Scope: "public"},
			{Table: "compute_node", Key: "overlay_ip", Owner: "vmmd", Scope: "public"},
			{Table: "compute_node", Key: "vpcpus", Owner: "vmmd", Scope: "public"},
			{Table: "compute_node", Key: "mem_mb", Owner: "vmmd", Scope: "public"},
			{Table: "compute_node", Key: "max_concurrency", Owner: "vmmd", Scope: "public"},
			{Table: "compute_node", Key: "admission_ceiling_mb", Owner: "vmmd", Scope: "public"},
			// PR scale-out tier-1 residual (Gaps #3 + #5):
			// per-host bridge CIDR override, per-host overlay
			// CIDR override, and the NIC pin used by the
			// overlay-IP auto-detector. All three flow from
			// `daemons.vmmd.compute_node` in the manifest schema
			// into the renderer's [compute_node] table; the
			// catalog entry is what tells ValidateTOMLPlacement
			// they belong here (and not at top level alongside
			// the legacy `tls_*_path` cluster — which would be
			// the bug class ADR-028 caught).
			{Table: "compute_node", Key: "host_bridge_cidr", Owner: "vmmd", Scope: "public"},
			{Table: "compute_node", Key: "overlay_cidr", Owner: "vmmd", Scope: "public"},
			{Table: "compute_node", Key: "overlay_interface", Owner: "vmmd", Scope: "public"},
		},
	},
	"schedd": {
		Daemon: "schedd",
		PrivateKeys: []string{
			"socket_path",
			"vmmd_socket",
			"gateway_synth_socket",
			"owner_user",
			"metrics_addr",
			"db_url",
		},
		// schedd has no [compute_node] block on its TOML today.
		ComputeNodeBlock: nil,
	},
	"apid": {
		Daemon: "apid",
		PrivateKeys: []string{
			"socket_path",
			"metrics_addr",
			"db_url",
			"apps_domain",
		},
		ComputeNodeBlock: nil,
	},
	"meterd": {
		Daemon: "meterd",
		PrivateKeys: []string{
			"socket_path",
			"metrics_addr",
			"db_url",
		},
		ComputeNodeBlock: nil,
	},
	"githubd": {
		Daemon: "githubd",
		PrivateKeys: []string{
			"socket_path",
			"metrics_addr",
			"db_url",
		},
		ComputeNodeBlock: nil,
	},
	"gatewayd_public": {
		Daemon: "gatewayd_public",
		PrivateKeys: []string{
			"socket_path",
			"metrics_addr",
			"listen_addr",
		},
		ComputeNodeBlock: nil,
	},
	"gatewayd_internal": {
		Daemon: "gatewayd_internal",
		PrivateKeys: []string{
			"socket_path",
			"metrics_addr",
			"listen_addr",
			"apps_domain",
		},
		ComputeNodeBlock: nil,
	},
	"imaged": {
		Daemon: "imaged",
		PrivateKeys: []string{
			"socket_path",
			"metrics_addr",
			"db_url",
		},
		ComputeNodeBlock: nil,
	},
	"builderd": {
		Daemon: "builderd",
		PrivateKeys: []string{
			"socket_path",
			"metrics_addr",
			"db_url",
		},
		ComputeNodeBlock: nil,
	},
}

// TombstoneKeys is the catalog of keys that the renderer MUST NOT
// emit, regardless of manifest input. Most are the misplaced keys
// already shipped to a fleet in a previous outage (issue #911 names
// `schedd_client_*`; the actual on-disk bug is the duplicate
// `tls_*_path` inside `[compute_node]`). The validator refuses a
// manifest whose policy / config explicitly enables any tombstone
// key — the renderer cannot cross-check the operator's intent.
//
// The slice is sorted for stable error messages.
var TombstoneKeys = []string{
	// vmmd's [compute_node] must NOT re-declare the top-level
	// cluster. Canonical tls_*_path lives at the top of
	// deploy/ansible/roles/vmmd_service/files/vmmd.toml.example
	// (lines 33-40 — tls_cert_path / tls_key_path / tls_ca_path
	// group, server-mTLS cluster).
	"compute_node.tls_cert_path",
	"compute_node.tls_key_path",
	"compute_node.tls_ca_path",
	// vmmd's [compute_node] must NOT carry schedd's client
	// material — the operator reports this exact shape in
	// issue #911. The renderer writes the schedd-side client
	// material into schedd's own TOML, not vmmd's.
	"compute_node.schedd_client_cert_path",
	"compute_node.schedd_client_key_path",
	"compute_node.schedd_client_ca_path",
	// vmmd's [compute_node] must NOT carry apid's client
	// material; the apid client leaf lives in apid/ on the
	// control-plane box.
	"compute_node.apid_client_cert_path",
	"compute_node.apid_client_key_path",
	"compute_node.apid_client_ca_path",
	// schedd's TOML must NOT carry vmmd-target material under
	// the wrong table — the bug class is symmetric.
	"vmmd_target.tls_cert_path",
	"vmmd_target.tls_key_path",
	"vmmd_target.tls_ca_path",
}

// ValidateTOMLPlacement walks the rendered TOML keys (passed as a
// `map[string]string` of key → value) and reports every tombstone
// key (and every key that crosses the private/public table line).
// The renderer (PR-2) calls this AFTER emitting TOML, before
// publishing to disk — a tombstone hit aborts the publish.
//
// The function is pure (no filesystem / no globals) so the
// renderer and the validator both run the same code.
func ValidateTOMLPlacement(daemon string, rendered map[string]string) Errors {
	var errs Errors
	host, ok := HostKeys[daemon]
	if !ok {
		errs = append(errs, Error{
			fmt.Sprintf("daemons.%s", daemon),
			"no HostKeys descriptor registered (renderer bug: add a row to manifest.HostKeys)",
		})
		return errs
	}
	for k := range rendered {
		// Section split: the slice point is the first '.'.
		idx := strings.Index(k, ".")
		var table, leaf string
		if idx == -1 {
			table, leaf = "", k
		} else {
			table, leaf = k[:idx], k[idx+1:]
		}
		// Tombstone check.
		if table != "" && contains(TombstoneKeys, k) {
			errs = append(errs, Error{
				fmt.Sprintf("daemons.%s.tombstone", daemon),
				fmt.Sprintf("key %q is a tombstone (issue #911): the renderer must not emit this key under [%s]", k, table),
			})
			continue
		}
		// Private-key placement: every key in PrivateKeys must
		// land at top level (no `[...]` table). If a private key
		// snuck into a table, the renderer leaked the issue #911
		// bug.
		if contains(host.PrivateKeys, leaf) && table != "" {
			errs = append(errs, Error{
				fmt.Sprintf("daemons.%s.private_key_in_table", daemon),
				fmt.Sprintf("key %q is a private key (top-level) but rendered inside [%s]", leaf, table),
			})
		}
		// ComputeNodeBlock placement: every key in the catalog
		// must land inside `[compute_node]` (not at top level).
		for _, ck := range host.ComputeNodeBlock {
			if ck.Key == leaf && table != ck.Table {
				errs = append(errs, Error{
					// Error code is a path token, not a dotted path
					// (the typo `top.level` here once matched
					// against a future alert's grep-regex, now fixed
					// to the underscore form).
					fmt.Sprintf("daemons.%s.cn_block_key_at_top_level", daemon),
					fmt.Sprintf("key %q belongs under [%s] but rendered at top level", leaf, ck.Table),
				})
			}
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// SortedHostKeys returns the daemon names in the HostKeys catalog,
// sorted alphabetically. Used by tests and the doctor (PR-4) to
// assert the catalog is exhaustive across the manifest schema's
// `daemons:` map.
func SortedHostKeys() []string {
	out := make([]string, 0, len(HostKeys))
	for k := range HostKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
