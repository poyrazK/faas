// coverage_test.go — fill the remaining pkg/daemonunitspec coverage
// gaps that registry_test.go deliberately doesn't touch. Targets
// the per-daemon UnitXxx() constructors and the load-bearing shape
// invariants on every one:
//
//   - Each daemon's unit shape (Description, ExecStart, Type,
//     Restart, WantedBy) is non-empty and well-formed.
//   - ExecStart points at /opt/faas/current/bin/.
//   - PrivateTmp invariant — load-bearing only for vmmd and
//     schedd (the documented cases at vmmd.go:18-22 referencing
//     run 30839233808). Other daemons have PrivateTmp=true by
//     default (their bind-mount / dial contract differs).
//   - For vmmd + schedd, ReadWritePaths must include /run/faas.
//   - Registry invariants: only imaged is best-effort, lifecycle
//     Probe is in the closed set, UnitByName surfaces every
//     descriptor plus an error for unknown names.
//
// Conventions: whitebox `package daemonunitspec` (matches the
// pre-existing *_test.go files).

package daemonunitspec

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// assertBasicShape is the unconditional shape check every daemon
// unit must satisfy.
func assertBasicShape(t *testing.T, name string, u daemonunit.Unit) {
	t.Helper()
	if u.Description == "" {
		t.Errorf("%s: Description empty", name)
	}
	if u.ExecStart == "" {
		t.Errorf("%s: ExecStart empty", name)
	}
	if !strings.HasPrefix(u.ExecStart, "/opt/faas/current/bin/") {
		t.Errorf("%s: ExecStart = %q, want /opt/faas/current/bin/ prefix", name, u.ExecStart)
	}
	if u.WantedBy != "multi-user.target" {
		t.Errorf("%s: WantedBy = %q, want multi-user.target", name, u.WantedBy)
	}
	if u.Type != "simple" {
		t.Errorf("%s: Type = %q, want simple", name, u.Type)
	}
	if u.Restart == "" {
		t.Errorf("%s: Restart empty", name)
	}
	// PrivateTmp must be a non-nil bool pointer (we neither
	// allow nil nor unset; every daemon gets an explicit value).
	if u.PrivateTmp == nil {
		t.Errorf("%s: PrivateTmp nil (must be explicit true or false)", name)
	}
}

func hasReadWrite(u daemonunit.Unit, path string) bool {
	for _, p := range u.ReadWritePaths {
		if p == path {
			return true
		}
	}
	return false
}

func hasEnvironment(u daemonunit.Unit, key, value string) bool {
	for _, env := range u.Environment {
		if env.Key == key && env.Value == value {
			return true
		}
	}
	return false
}

func hasLoadCredential(u daemonunit.Unit, name, path string) bool {
	for _, cred := range u.LoadCredential {
		if cred.Name == name && cred.Path == path && !cred.Optional {
			return true
		}
	}
	return false
}

func hasOptionalLoadCredential(u daemonunit.Unit, name, path string) bool {
	for _, cred := range u.LoadCredential {
		if cred.Name == name && cred.Path == path && cred.Optional {
			return true
		}
	}
	return false
}

// --- per-daemon UnitXxx() constructors ------------------------------

func TestUnitVmmd_Shape(t *testing.T) {
	u := UnitVmmd()
	assertBasicShape(t, "vmmd", u)
	// vmmd is the only documented PrivateTmp=false case
	// (wipe-comment at vmmd.go:18-22 referencing run 30839233808).
	if u.PrivateTmp == nil || *u.PrivateTmp {
		t.Errorf("vmmd: PrivateTmp = %v, want BoolPtr(false) (load-bearing bug per wipe-comment run 30839233808)", u.PrivateTmp)
	}
	if !hasReadWrite(u, "/run/faas") {
		t.Errorf("vmmd: missing ReadWritePaths=/run/faas")
	}
	if !hasReadWrite(u, "/srv/fc") {
		t.Errorf("vmmd: missing ReadWritePaths=/srv/fc (jailer tmpfs)")
	}
	// vmmd is the only root component — no User/Group by design.
	if u.User != "" {
		t.Errorf("vmmd: User = %q, want empty (root by design)", u.User)
	}
	if u.Slice != FaasCPSlice {
		t.Errorf("vmmd: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
	if len(u.AmbientCapabilities) == 0 {
		t.Error("vmmd: AmbientCapabilities empty (CAP_NET_BIND_SERVICE expected)")
	}
	// ExecStartPre must include the /run/faas chown/chmod
	// re-assertion (load-bearing — wipes any hand-edit drift).
	found := false
	for _, c := range u.ExecStartPre {
		if strings.Contains(c, "/run/faas") {
			found = true
		}
	}
	if !found {
		t.Errorf("vmmd: ExecStartPre missing /run/faas hook: %v", u.ExecStartPre)
	}
}

func TestUnitApid_Shape(t *testing.T) {
	u := UnitApid()
	assertBasicShape(t, "apid", u)
	if u.User != "faas-apid" {
		t.Errorf("apid: User = %q, want faas-apid", u.User)
	}
	if u.Slice != FaasCPSlice {
		t.Errorf("apid: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
	if !hasEnvironment(u, "FAAS_HOST_HMAC_KEY_PATH", "%d/faas_host_hmac_key") {
		t.Error("apid: missing FAAS_HOST_HMAC_KEY_PATH credential-dir environment")
	}
	if !hasLoadCredential(u, "faas_host_hmac_key", "/etc/faas/secrets/host.hmac.key") {
		t.Error("apid: missing required faas_host_hmac_key LoadCredential")
	}
	if !hasEnvironment(u, "FAAS_LOG_ARCHIVE_CREDS_PATH", "%d/faas_archive_creds") {
		t.Error("apid: missing optional log archive credential-dir environment")
	}
	if !hasOptionalLoadCredential(u, "faas_archive_creds", "/etc/faas/secrets/storage-box/archive-creds.json") {
		t.Error("apid: missing optional faas_archive_creds LoadCredential")
	}
}

func TestUnitSchedd_Shape(t *testing.T) {
	u := UnitSchedd()
	assertBasicShape(t, "schedd", u)
	// schedd is the second documented PrivateTmp=false case
	// (wipe-comment at schedd.go:8-18 referencing run 30839233808).
	if u.PrivateTmp == nil || *u.PrivateTmp {
		t.Errorf("schedd: PrivateTmp = %v, want BoolPtr(false) (load-bearing bug per wipe-comment run 30839233808)", u.PrivateTmp)
	}
	if !hasReadWrite(u, "/run/faas") {
		t.Errorf("schedd: missing ReadWritePaths=/run/faas")
	}
	if u.User != "faas-schedd" {
		t.Errorf("schedd: User = %q, want faas-schedd", u.User)
	}
	if u.Slice != FaasCPSlice {
		t.Errorf("schedd: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
	if u.MemoryMax == "" {
		t.Error("schedd: MemoryMax empty")
	}
}

func TestUnitBuilderd_Shape(t *testing.T) {
	// builderd is best-effort with respect to apid ordering;
	// the comment at registry.go:67-82 explains why.
	u := UnitBuilderd()
	assertBasicShape(t, "builderd", u)
	if u.User != "faas-builderd" {
		t.Errorf("builderd: User = %q", u.User)
	}
	if u.Slice != FaasCPSlice {
		t.Errorf("builderd: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
	// Pin After does NOT contain apid (cross-host split-box
	// ordering — comment at registry.go:73-81).
	for _, a := range u.After {
		if a == "apid" {
			t.Errorf("builderd: After must NOT include apid (cross-host split-box): got %v", u.After)
		}
	}
	if !hasReadWrite(u, "/var/cache/faas/builds") {
		t.Error("builderd: missing ReadWritePaths=/var/cache/faas/builds")
	}
}

func TestUnitGatewaydInternal_Shape(t *testing.T) {
	u := UnitGatewaydInternal()
	assertBasicShape(t, "gatewayd-internal", u)
	if u.Slice != FaasCPSlice {
		t.Errorf("gatewayd-internal: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
	if !hasEnvironment(u, "FAAS_LOG_ARCHIVE_CREDS_PATH", "%d/faas_archive_creds") {
		t.Error("gatewayd-internal: missing optional log archive credential-dir environment")
	}
	if !hasOptionalLoadCredential(u, "faas_archive_creds", "/etc/faas/secrets/storage-box/archive-creds.json") {
		t.Error("gatewayd-internal: missing optional faas_archive_creds LoadCredential")
	}
}

func TestUnitGatewaydPublic_Shape(t *testing.T) {
	u := UnitGatewaydPublic()
	assertBasicShape(t, "gatewayd-public", u)
	if u.Slice != FaasCPSlice {
		t.Errorf("gatewayd-public: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
}

func TestUnitGithubd_Shape(t *testing.T) {
	u := UnitGithubd()
	assertBasicShape(t, "githubd", u)
	// githubd's User is "faas" (not "faas-githubd") per
	// githubd.go:39 — the daemon runs under the shared
	// group-managed user identity.
	if u.User != "faas" {
		t.Errorf("githubd: User = %q, want faas", u.User)
	}
	if u.Slice != FaasCPSlice {
		t.Errorf("githubd: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
	if !hasReadWrite(u, "/var/lib/faas") {
		t.Errorf("githubd: missing ReadWritePaths=/var/lib/faas (attribution hmac + bot creds)")
	}
	// githubd owns /run/faas/githubd.sock — pin it lists /run/faas
	// in ReadWritePaths (the bug class is PrivateTmp=true on the
	// home unit, which only breaks siblings; sibling-safe here).
	if !hasReadWrite(u, "/run/faas") {
		t.Errorf("githubd: missing ReadWritePaths=/run/faas (githubd.sock home)")
	}
}

func TestUnitImaged_Shape(t *testing.T) {
	// imaged is the only best-effort (Critical:false) daemon.
	// Pin its unit shape anyway — Regression finding E1.
	u := UnitImaged()
	assertBasicShape(t, "imaged", u)
	if u.User != "faas-imaged" {
		t.Errorf("imaged: User = %q", u.User)
	}
	if u.Slice != FaasCPSlice {
		t.Errorf("imaged: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
	// imaged does NOT dial /run/faas sockets — it talks to vmmd
	// over faas-cp.slice dependency instead. Pin the FAAS_BASE_*
	// staging-root env wiring (load-bearing per imaged.go:22-25).
	for _, kv := range u.Environment {
		if kv.Key == "FAAS_BASE_STAGING_ROOT" && kv.Value != "/dev/shm/faas-base-staging" {
			t.Errorf("imaged: FAAS_BASE_STAGING_ROOT = %q, want /dev/shm/faas-base-staging (tmpfs — ext4 /tmp breaks overlay mount)", kv.Value)
		}
	}
}

func TestUnitMeterd_Shape(t *testing.T) {
	u := UnitMeterd()
	assertBasicShape(t, "meterd", u)
	if u.User != "faas-meterd" {
		t.Errorf("meterd: User = %q", u.User)
	}
	if u.Slice != FaasCPSlice {
		t.Errorf("meterd: Slice = %q, want %q", u.Slice, FaasCPSlice)
	}
	if !hasReadWrite(u, "/var/log/faas") {
		t.Errorf("meterd: missing ReadWritePaths=/var/log/faas")
	}
}

// --- Registry invariants --------------------------------------------

func TestRegistry_OnlyOneBestEffort(t *testing.T) {
	// Pin the Critical-vs-best-effort split: only imaged is
	// best-effort (the dep that pre-dates the ADR-110 builderd
	// addition). Adding a second best-effort daemon must surface
	// here.
	var best []string
	for _, e := range Registry {
		if !e.Critical {
			best = append(best, e.Name)
		}
	}
	if len(best) != 1 || best[0] != "imaged" {
		t.Errorf("best-effort daemons = %v, want [imaged]", best)
	}
}

func TestRegistry_LifecycleProbesAreAllKnown(t *testing.T) {
	// Pin the closed set of Probe values (registry.go:32-36).
	known := map[Probe]bool{ProbeSystemd: true, ProbeUnix: true, ProbeTCP: true}
	for _, e := range Registry {
		if !known[e.Lifecycle.Probe] {
			t.Errorf("%s: lifecycle.Probe = %q, want one of systemd/unix/tcp", e.Name, e.Lifecycle.Probe)
		}
		// TCP and Unix probes must carry a ProbeTarget.
		if e.Lifecycle.Probe != ProbeSystemd && e.Lifecycle.Probe != "" {
			if e.Lifecycle.ProbeTarget == "" {
				t.Errorf("%s: ProbeTarget empty for probe %q", e.Name, e.Lifecycle.Probe)
			}
		}
		if e.Lifecycle.ReadyzURL == "" {
			t.Errorf("%s: ReadyzURL must be declared for deploy verification", e.Name)
		}
	}
}

func TestRegistry_UnitConstructorsNonNil(t *testing.T) {
	// registry.go:23-28 — every Entry must carry a Unit
	// constructor. A nil value here surfaces as a renderer nil
	// deref at cd-controlplane restart-loop emit time.
	for _, e := range Registry {
		if e.Unit == nil {
			t.Errorf("%s: Unit constructor nil", e.Name)
		}
	}
}

// --- UnitByName + unknown error path -------------------------------

func TestUnitByName_EveryRegistryEntryResolves(t *testing.T) {
	for _, e := range Registry {
		got, err := UnitByName(e.Name)
		if err != nil {
			t.Errorf("UnitByName(%q): %v", e.Name, err)
			continue
		}
		if got.Description == "" {
			t.Errorf("UnitByName(%q): empty Description", e.Name)
		}
	}
}

func TestUnitByName_UnknownReturnsErrorListingKnown(t *testing.T) {
	// registry.go:101 — the unknown-daemon error message lists
	// the known descriptors so a renderer reaches a useful
	// debugging surface.
	_, err := UnitByName("not-a-daemon")
	if err == nil {
		t.Fatal("err = nil, want unknown-daemon error")
	}
	for _, e := range Registry {
		if !strings.Contains(err.Error(), e.Name) {
			t.Errorf("err = %v, want %q in chain", err, e.Name)
		}
	}
}

// --- FaasCPSlice constant -------------------------------------------

func TestFaasCPSlice_HardcodedThreeGigabytes(t *testing.T) {
	// Per registry.go:104-117 — the comment says the slice is a
	// known under-utilisation vs the financial model's 6 GB. Pin
	// the constant value so a future merge that "tweaks" the
	// memory max surfaces here rather than silently capping the
	// slice smaller (or larger).
	if FaasCPSlice != "faas-cp.slice" {
		t.Errorf("FaasCPSlice = %q, want faas-cp.slice", FaasCPSlice)
	}
}
