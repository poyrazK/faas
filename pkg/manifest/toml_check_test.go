// coverage_test.go — fill the remaining pkg/manifest coverage gaps
// that manifest_test.go deliberately doesn't touch. Targets:
//
//   - toml_check.go (365 LOC, 0% covered) — ValidateTOMLPlacement
//     across all five branches (unknown daemon, tombstone key,
//     private-key-in-table, cn-block-key-at-top-level, happy path),
//     TableKey.String rendering, SortedHostKeys. Plus the
//     HostKeys catalog invariant that pins every known daemon has
//     a descriptor.
//
// Conventions: whitebox `package manifest` (matches the
// pre-existing manifest_test.go which is also whitebox).

package manifest

import (
	"sort"
	"strings"
	"testing"
)

// --- TableKey.String (toml_check.go:66) -----------------------------

func TestTableKey_String(t *testing.T) {
	cases := []struct {
		tk   TableKey
		want string
	}{
		{TableKey{Key: "tls_cert_path"}, "tls_cert_path"},
		{TableKey{Table: "compute_node", Key: "name"}, "compute_node.name"},
		{TableKey{Table: "", Key: "leaf"}, "leaf"},
	}
	for _, c := range cases {
		if got := c.tk.String(); got != c.want {
			t.Errorf("TableKey{%q,%q}.String() = %q, want %q", c.tk.Table, c.tk.Key, got, c.want)
		}
	}
}

// --- SortedHostKeys (toml_check.go:358) ----------------------------

func TestSortedHostKeys_Alphabetical(t *testing.T) {
	// Pin the sorted output preserves the catalog count and
	// returns alphabetical order. Used by the doctor (PR-4) to
	// drive a check that every manifest-schema daemon has a
	// HostKeys descriptor.
	got := SortedHostKeys()
	if len(got) != len(HostKeys) {
		t.Fatalf("SortedHostKeys len = %d, want %d", len(got), len(HostKeys))
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("SortedHostKeys not sorted: %v", got)
	}
	// Spot-check that known descriptors appear.
	for _, want := range []string{"vmmd", "schedd", "apid"} {
		found := false
		for _, k := range got {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SortedHostKeys missing %q: %v", want, got)
		}
	}
}

// --- ValidateTOMLPlacement (toml_check.go:296) ---------------------

func TestCoverageTOMLPlacement_UnknownDaemon(t *testing.T) {
	// Pin the "no HostKeys descriptor" branch at line 300-304.
	errs := ValidateTOMLPlacement("does-not-exist", map[string]string{"x": "y"})
	if len(errs) != 1 {
		t.Fatalf("errs len = %d, want 1: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Path, "does-not-exist") {
		t.Errorf("err path = %q, want daemon name", errs[0].Path)
	}
	if !strings.Contains(errs[0].Message, "no HostKeys descriptor") {
		t.Errorf("err reason = %q", errs[0].Message)
	}
}

func TestCoverageTOMLPlacement_TombstoneKey(t *testing.T) {
	// Pin toml_check.go:316-322 — a render emitting a tombstone
	// key inside any table aborts the publish.
	for _, k := range []string{
		"compute_node.tls_cert_path",
		"compute_node.tls_key_path",
		"compute_node.schedd_client_cert_path",
		"compute_node.apid_client_ca_path",
		"vmmd_target.tls_cert_path",
	} {
		errs := ValidateTOMLPlacement("vmmd", map[string]string{k: "x"})
		if len(errs) != 1 {
			t.Errorf("k=%q: errs len = %d, want 1: %v", k, len(errs), errs)
			continue
		}
		if !strings.Contains(errs[0].Message, "tombstone") {
			t.Errorf("k=%q: reason = %q, want tombstone", k, errs[0].Message)
		}
	}
}

func TestCoverageTOMLPlacement_PrivateKeyInTable(t *testing.T) {
	// Pin toml_check.go:327-332 — a daemon-private key snuck into
	// a TOML table. The bug class is the inverse of issue #911:
	// the renderer leaks the daemon's own listener key into
	// [compute_node]. Note: the tombstone check at line 316 runs
	// first and `continue`s, so we test with private keys NOT in
	// TombstoneKeys to reach the private-key branch.
	for _, k := range []string{
		"compute_node.socket_path",
		"compute_node.metrics_addr",
		"compute_node.db_url",
		"compute_node.owner_user",
		"compute_node.kernel_path",
		"compute_node.listen_addr",
	} {
		errs := ValidateTOMLPlacement("vmmd", map[string]string{k: "x"})
		if len(errs) != 1 {
			t.Errorf("k=%q: errs len = %d, want 1: %v", k, len(errs), errs)
			continue
		}
		if !strings.Contains(errs[0].Message, "private key (top-level)") {
			t.Errorf("k=%q: reason = %q", k, errs[0].Message)
		}
		if !strings.Contains(errs[0].Message, "compute_node") {
			t.Errorf("k=%q: reason missing table name: %q", k, errs[0].Message)
		}
	}
}

func TestCoverageTOMLPlacement_CNBlockKeyAtTopLevel(t *testing.T) {
	// Pin toml_check.go:334-346 — a [compute_node]-bound key
	// rendered at top level. Same bug class, mirror of the
	// private-key branch.
	for _, k := range []string{
		"target_url",
		"overlay_ip",
		"vpcpus",
		"mem_mb",
		"max_concurrency",
		"admission_ceiling_mb",
		"host_bridge_cidr",
		"overlay_cidr",
		"overlay_interface",
	} {
		errs := ValidateTOMLPlacement("vmmd", map[string]string{k: "x"})
		if len(errs) != 1 {
			t.Errorf("k=%q: errs len = %d, want 1: %v", k, len(errs), errs)
			continue
		}
		if !strings.Contains(errs[0].Message, "[compute_node]") {
			t.Errorf("k=%q: reason missing table name: %q", k, errs[0].Message)
		}
		if !strings.Contains(errs[0].Message, "top level") {
			t.Errorf("k=%q: reason missing 'top level': %q", k, errs[0].Message)
		}
	}
}

func TestCoverageTOMLPlacement_HappyPath(t *testing.T) {
	// Construct a fully-valid vmmd-rendered key set: top-level
	// private keys + [compute_node] keys, properly placed. Pin
	// nil error.
	good := map[string]string{
		// Top-level private cluster (vmmd's own listener + TLS).
		"socket_path":   "/run/vmmd.sock",
		"metrics_addr":  ":9090",
		"db_url":        "postgres://...",
		"owner_user":    "vmmd",
		"kernel_path":   "/var/lib/faas/vmlinux",
		"listen_addr":   ":8080",
		"tls_cert_path": "/etc/faas/vmmd/cert.pem",
		"tls_key_path":  "/etc/faas/vmmd/key.pem",
		"tls_ca_path":   "/etc/faas/vmmd/ca.pem",
		// [compute_node] public cluster (self-registration identity).
		"compute_node.name":                 "vmmd-1",
		"compute_node.target_url":           "https://10.0.0.1:8080",
		"compute_node.overlay_ip":           "10.0.0.1",
		"compute_node.vpcpus":               "4",
		"compute_node.mem_mb":               "2048",
		"compute_node.max_concurrency":      "20",
		"compute_node.admission_ceiling_mb": "47600",
		"compute_node.host_bridge_cidr":     "10.99.0.0/16",
		"compute_node.overlay_cidr":         "10.100.0.0/16",
		"compute_node.overlay_interface":    "eth0",
	}
	errs := ValidateTOMLPlacement("vmmd", good)
	if errs != nil {
		t.Errorf("happy path returned errors: %v", errs)
	}
}

func TestCoverageTOMLPlacement_MixedErrorsReportedTogether(t *testing.T) {
	// Pin that multiple distinct error paths surface in a single
	// call (renderer emits multiple bad keys at once → one
	// publish aborted).
	mixed := map[string]string{
		"compute_node.tls_cert_path":           "x", // tombstone + private-in-table
		"compute_node.schedd_client_cert_path": "y", // tombstone
		"target_url":                           "z", // cn-block-key-at-top-level
		"good":                                 "v", // fine
	}
	errs := ValidateTOMLPlacement("vmmd", mixed)
	if len(errs) != 3 {
		t.Errorf("errs len = %d, want 3: %v", len(errs), errs)
	}
}

func TestCoverageTOMLPlacement_AllDaemonsHaveDescriptor(t *testing.T) {
	// Pin the catalog invariant: every daemon the schema knows
	// about has a HostKeys row. A future schema addition that
	// forgets to update HostKeys breaks here, not at runtime.
	// The hostKeys catalog is the meta-descriptor for
	// ValidateTOMLPlacement. Note this test pins the closed-set
	// invariant that the renderer (PR-2) and the doctor (PR-4)
	// also depend on.
	knownFromManifest := []string{
		"vmmd", "schedd", "apid", "meterd", "githubd",
		"gatewayd_public", "gatewayd_internal",
		"imaged", "builderd", "realtimed",
	}
	for _, name := range knownFromManifest {
		hb, ok := HostKeys[name]
		if !ok {
			t.Errorf("daemon %q missing from HostKeys catalog", name)
			continue
		}
		if hb.Daemon != name {
			t.Errorf("daemon %q: HostKeys.Daemon = %q", name, hb.Daemon)
		}
		// vmmd is the only daemon with a ComputeNodeBlock today;
		// every other daemon's ComputeNodeBlock must be nil.
		if name != "vmmd" && hb.ComputeNodeBlock != nil {
			t.Errorf("daemon %q: unexpected ComputeNodeBlock: %v", name, hb.ComputeNodeBlock)
		}
	}
}

func TestCoverageTOMLPlacement_NoPrivateKeysForLeafOnlyDaemons(t *testing.T) {
	// schedd has its own PrivateKeys cluster. Pin the validator
	// catches a private-key-in-table for schedd similarly to vmmd.
	errs := ValidateTOMLPlacement("schedd", map[string]string{
		"compute_node.socket_path": "x", // private-in-table
	})
	if len(errs) != 1 {
		t.Errorf("schedd private-key-in-table: errs len = %d, want 1: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "private key") {
		t.Errorf("reason missing private-key label: %q", errs[0].Message)
	}
}

// --- TombstoneKeys closed set --------------------------------------

func TestTombstoneKeys_NoDuplicates(t *testing.T) {
	// Pin the closed-set invariant at toml_check.go:259 — the
	// TombstoneKeys slice is intentionally exhaustive; adding the
	// same key twice would cause ValidateTOMLPlacement to emit
	// duplicate errors. Pin the dedup invariant.
	seen := map[string]bool{}
	for _, k := range TombstoneKeys {
		if seen[k] {
			t.Errorf("duplicate tombstone key %q", k)
		}
		seen[k] = true
	}
}

func TestTombstoneKeys_AllTableQualified(t *testing.T) {
	// Tombstones are table-qualified (the issue #911 class is
	// "key landed inside a table it doesn't belong in"); pin
	// every entry has at least one '.' so the validator's
	// `table != ""` gate at line 316 doesn't bypass the check.
	for _, k := range TombstoneKeys {
		if !strings.Contains(k, ".") {
			t.Errorf("TombstoneKeys entry %q must be table-qualified", k)
		}
	}
}
