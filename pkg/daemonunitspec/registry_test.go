package daemonunitspec

import (
	"net"
	"net/url"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

func TestRegistryEntriesHaveBootProbes(t *testing.T) {
	for _, entry := range Registry {
		t.Run(entry.Name, func(t *testing.T) {
			switch entry.Lifecycle.Probe {
			case ProbeSystemd:
			case ProbeUnix, ProbeTCP:
				if entry.Lifecycle.ProbeTarget == "" {
					t.Fatal("transport probe has an empty target")
				}
			default:
				t.Fatalf("missing or unknown lifecycle probe %q", entry.Lifecycle.Probe)
			}

			u, err := url.Parse(entry.Lifecycle.ReadyzURL)
			if err != nil {
				t.Fatalf("parse ReadyzURL: %v", err)
			}
			if u.Scheme != "http" || u.Path != "/readyz" {
				t.Fatalf("ReadyzURL = %q, want loopback http /readyz", entry.Lifecycle.ReadyzURL)
			}
			ip := net.ParseIP(u.Hostname())
			if ip == nil || !ip.IsLoopback() {
				t.Fatalf("ReadyzURL host = %q, want loopback IP", u.Hostname())
			}
		})
	}
}

func TestActivationOrder(t *testing.T) {
	got := ActivationOrder()
	want := []string{"vmmd", "realtimed", "apid", "schedd", "gatewayd-internal", "gatewayd-public", "meterd", "githubd", "imaged", "builderd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActivationOrder() = %v, want %v", got, want)
	}
}

func TestLifecycleDependenciesAndProbes(t *testing.T) {
	byName := make(map[string]Entry, len(Registry))
	for _, entry := range Registry {
		byName[entry.Name] = entry
	}
	if got := byName["schedd"].Lifecycle.After; !reflect.DeepEqual(got, []string{"vmmd"}) {
		t.Fatalf("schedd dependencies = %v", got)
	}
	if got := byName["gatewayd-internal"].Lifecycle.ProbeTarget; got != "127.0.0.1:9090" {
		t.Fatalf("gatewayd-internal probe = %q", got)
	}
	if got := byName["gatewayd-public"].Lifecycle.ProbeTarget; got != "127.0.0.1:8080" {
		t.Fatalf("gatewayd-public probe = %q", got)
	}
}

// TestRegistryIsSubsetOfHostKeys pins the catalog-parity invariant
// between the daemonunitspec.Registry (systemd-managed daemons, drives
// the renderer + the deploy generator) and pkg/manifest.HostKeys (the
// TOML key catalog, drives the validator).
//
// The Registry MUST be a subset of HostKeys — every Registry entry
// has a manifest.DaemonConfig row + a HostKeys row, otherwise the
// renderer's renderTOML / ValidateTOMLPlacement call will fail with
// "no HostKeys descriptor". The reverse is NOT required: HostKeys
// may carry daemons that aren't systemd-managed (e.g. builderd —
// vmmd spawns it per-build inside an ephemeral microVM per ADR-003).
//
// A future change that adds a daemon to the Registry without a
// matching HostKeys row will trip this test before it reaches CI.
func TestRegistryIsSubsetOfHostKeys(t *testing.T) {
	registry := map[string]bool{}
	for _, e := range Registry {
		registry[e.Name] = true
	}
	// The Registry uses dashed names (gatewayd-internal); the
	// HostKeys catalog uses underscored names (gatewayd_internal).
	// Translate the Registry set before the subset check.
	translated := make(map[string]bool, len(registry))
	for name := range registry {
		switch name {
		case "gatewayd-internal":
			translated["gatewayd_internal"] = true
		case "gatewayd-public":
			translated["gatewayd_public"] = true
		default:
			translated[name] = true
		}
	}
	for hostKey := range manifest.HostKeys {
		if !translated[hostKey] {
			// HostKeys entry with no Registry match is fine
			// (builderd is the canonical example). Surface it
			// in test output so a future maintainer can audit
			// the asymmetry against the per-daemon decision
			// documented in daemonunitspec.go.
			t.Logf("HostKeys entry %q has no Registry counterpart (OK if per-build or future-spawn)", hostKey)
		}
	}
	for name := range translated {
		if _, ok := manifest.HostKeys[name]; !ok {
			t.Errorf("Registry name %q has no HostKeys row — renderer will fail at renderTOML", name)
		}
	}
}

// TestRegistryDaemonSet_LockstepWithHostKeys is the load-bearing
// cardinality assertion for issue #911 / ADR-110 (PR-6). It is
// stronger than TestRegistryIsSubsetOfHostKeys above:
//
//   - Pins |Registry| = 9 (the systemd-managed daemon set).
//   - Pins |manifest.HostKeys| = 9 (no extra — the schema catalog
//     and the daemon registry are now lockstep).
//   - Pins the named except-set: HostKeys \ Registry == {} (empty).
//     Mega-PR-C moved builderd from the "ephemeral per-build" except
//     set into the Registry proper, alongside its daemonunitspec
//     constructor (pkg/daemonunitspec/builderd.go::UnitBuilderd),
//     its deploy role (deploy/ansible/roles/builderd_service/),
//     and its deploy/systemd/ tree copy.
//
// Why the cardinality check matters: a future PR that adds a daemon
// to the Registry MUST also add it to manifest.HostKeys (renderer
// reads HostKeys; without the row, renderTOML fails at runtime).
// The reverse — adding a HostKey without a Registry entry — is
// allowed but only for ADR-documented ephemeral daemons; the
// previous canonical example was builderd, which Mega-PR-C
// promoted out of the except-set.
//
// Reuses the dashed→underscored translation from the test above:
// gatewayd-internal → gatewayd_internal, gatewayd-public →
// gatewayd_public; rest are identity.
func TestRegistryDaemonSet_LockstepWithHostKeys(t *testing.T) {
	const (
		wantRegistrySize = 10
		wantHostKeysSize = 10
	)
	if got := len(Registry); got != wantRegistrySize {
		t.Errorf("Registry len = %d, want %d (adding a daemon requires updating manifest.HostKeys too)",
			got, wantRegistrySize)
	}
	registrySet := make(map[string]bool, len(Registry))
	for _, e := range Registry {
		// Apply the same dashed→underscored translation as
		// TestRegistryIsSubsetOfHostKeys so the set comparison
		// uses the HostKeys convention.
		switch e.Name {
		case "gatewayd-internal":
			registrySet["gatewayd_internal"] = true
		case "gatewayd-public":
			registrySet["gatewayd_public"] = true
		default:
			registrySet[e.Name] = true
		}
	}
	if got := len(manifest.HostKeys); got != wantHostKeysSize {
		t.Errorf("manifest.HostKeys len = %d, want %d (Registry=%d, lockstep cardinality)",
			got, wantHostKeysSize, len(Registry))
	}
	// Build the symmetric difference: HostKeys \ Registry. Mega-PR-C
	// moved builderd from the except-set into the Registry, so this
	// is now expected to be empty. A future non-systemd ephemeral
	// daemon (e.g. a per-build ephemeral sshd) would re-introduce
	// an entry here and require an ADR-documented exception.
	extra := make(map[string]bool)
	for hk := range manifest.HostKeys {
		if !registrySet[hk] {
			extra[hk] = true
		}
	}
	if len(extra) != 0 {
		names := make([]string, 0, len(extra))
		for n := range extra {
			names = append(names, n)
		}
		t.Errorf("HostKeys \\ Registry has %d entries (%v), want empty. "+
			"Mega-PR-C promoted builderd into the Registry; a future entry here "+
			"requires an ADR-documented ephemeral exception.",
			len(extra), names)
	}
	// Reverse direction — every Registry entry must have a HostKeys
	// row (preserved from TestRegistryIsSubsetOfHostKeys).
	for name := range registrySet {
		if _, ok := manifest.HostKeys[name]; !ok {
			t.Errorf("Registry name %q has no HostKeys row — renderer will fail at renderTOML", name)
		}
	}
}
