// manifest_from_image_config_test.go — focused coverage of the
// post-M-1 manifest derivation contract.
//
// Two paths converge on oci.ManifestFromConfig today:
//   - registry pull (handler.go::manifestFromImageConfig) for the
//     `App` deployment mode, where ImageConfig comes from the
//     registry puller
//   - source build (local_oci.go::buildLocalOCIAppLayer) for source-
//     built apps, where Config comes from builderd's exported tarball
//
// Both paths share oci.ManifestFromConfig's:
//   - Entrypoint+Cmd joined semantics
//   - Healthcheck/StopSignal/StopGracePeriod projection
//   - ErrImageManifestInvalid failure shape on empty-image configs
//
// The tests below pin the ImageConfig-side surface (registry path)
// since the Config-side surface is already covered by
// pkg/oci/parse_unified_test.go and pkg/oci/image_test.go.
package imaged

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
)

func TestManifestFromImageConfig_AlpineEntrypointOnly(t *testing.T) {
	t.Parallel()
	// Alpine 3.19 declares `ENTRYPOINT ["/bin/sh"]` and no CMD —
	// before M-1 this surfaced as Entrypoint=[] (a no-arg exec);
	// commit 5 closes that gap (ADR-136 §Decision 1).
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Entrypoint: []string{"/bin/sh"},
		Env:        map[string]string{"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if got, want := m.Entrypoint, []string{"/bin/sh"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Entrypoint = %v; want %v", got, want)
	}
}

func TestManifestFromImageConfig_DistrolessEntrypointPlusCmd(t *testing.T) {
	t.Parallel()
	// Distroless static-debian12 declares both ENTRYPOINT and CMD.
	// OCI semantics: argv = entrypoint + cmd. Before M-1 only
	// cfg.Cmd was consumed; commit 5 surfaces both.
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Entrypoint: []string{"/app"},
		Cmd:        []string{"run"},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if got, want := m.Entrypoint, []string{"/app", "run"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Entrypoint = %v; want %v", got, want)
	}
}

func TestManifestFromImageConfig_BusyboxCmdOnly(t *testing.T) {
	t.Parallel()
	// Busybox 1.36 declares `CMD ["sh"]` only — argv becomes ["sh"].
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd: []string{"sh"},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if got, want := m.Entrypoint, []string{"sh"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Entrypoint = %v; want %v", got, want)
	}
}

func TestManifestFromImageConfig_SingleTCPExposeSeedsPortAndEnv(t *testing.T) {
	t.Parallel()
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd:          []string{"/app/server"},
		ExposedPorts: map[string]struct{}{"3000/tcp": {}},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.Port != 3000 || m.Env["PORT"] != "3000" {
		t.Fatalf("manifest port/env = %d/%q, want 3000/3000", m.Port, m.Env["PORT"])
	}
}

func TestManifestFromImageConfig_AmbiguousExposeKeepsDefaultPort(t *testing.T) {
	t.Parallel()
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd:          []string{"/app/server"},
		ExposedPorts: map[string]struct{}{"3000/tcp": {}, "8080/tcp": {}},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.Port != 0 || m.EffectivePort() != 8080 || m.Env["PORT"] != "8080" {
		t.Fatalf("manifest port/effective/env = %d/%d/%q, want 0/8080/8080", m.Port, m.EffectivePort(), m.Env["PORT"])
	}
}

func TestManifestFromImageConfig_HealthcheckFlowThrough(t *testing.T) {
	t.Parallel()
	// HEALTHCHECK CMD shape projects onto AppManifest.Healthcheck
	// (issue #1186 workstream A.4 / ADR-136 §Decision 4).
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd: []string{"/app/server"},
		Healthcheck: &oci.ImageHealthcheck{
			Test:         []string{"CMD", "/bin/check"},
			IntervalS:    30,
			TimeoutS:     5,
			Retries:      3,
			StartPeriodS: 10,
		},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.Healthcheck == nil {
		t.Fatal("Healthcheck nil; want populated")
	}
	if got, want := m.Healthcheck.IntervalS, 30; got != want {
		t.Errorf("Healthcheck.IntervalS = %d; want %d", got, want)
	}
	if got, want := m.Healthcheck.Retries, 3; got != want {
		t.Errorf("Healthcheck.Retries = %d; want %d", got, want)
	}
}

func TestManifestFromImageConfig_HealthcheckNoneFlows(t *testing.T) {
	t.Parallel()
	// HEALTHCHECK NONE projects to a non-nil Healthcheck with
	// Test=["NONE"] — distinguishes "explicitly disabled" from
	// "image didn't declare one" (commit 4 decision).
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd:         []string{"/app/server"},
		Healthcheck: &oci.ImageHealthcheck{Test: []string{"NONE"}},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.Healthcheck == nil || len(m.Healthcheck.Test) != 1 || m.Healthcheck.Test[0] != "NONE" {
		t.Errorf("Healthcheck = %+v; want Test=[NONE]", m.Healthcheck)
	}
}

func TestManifestFromImageConfig_StopSignalFlows(t *testing.T) {
	t.Parallel()
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd:        []string{"/app/server"},
		StopSignal: "SIGUSR1",
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.StopSignal != "SIGUSR1" {
		t.Errorf("StopSignal = %q; want SIGUSR1", m.StopSignal)
	}
}

func TestManifestFromImageConfig_NumericUserFlows(t *testing.T) {
	t.Parallel()
	// Distroless USER 65532 → AppManifest.User="65532". Numeric
	// passthrough; named-user resolution is M-3 (ADR-136 §Decision 2).
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd:  []string{"/app/server"},
		User: "65532",
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.User != "65532" {
		t.Errorf("User = %q; want 65532", m.User)
	}
}

func TestManifestFromImageConfig_NoEntrypointOrCmd_ErrInvalid(t *testing.T) {
	t.Parallel()
	// An image that declares neither Entrypoint nor Cmd is rejected
	// at the helper boundary with oci.ErrImageManifestInvalid so the
	// deploy path surfaces a stable error code.
	_, err := manifestFromImageConfig(oci.ImageConfig{
		WorkingDir: "/app",
	})
	if err == nil {
		t.Fatal("err = nil; want ErrImageManifestInvalid")
	}
	if !errors.Is(err, oci.ErrImageManifestInvalid) {
		t.Errorf("err = %v; want ErrImageManifestInvalid", err)
	}
}

// --- fixture-driven cross-cutting coverage ---------------------------
//
// The fixtures below come from pkg/imaged/testdata/oci-fixtures/ and
// pin the canonical registry-image shapes against which the M-1
// acceptance criterion ("Standard images using ENTRYPOINT, CMD, USER,
// and WORKDIR run with the expected behavior") is measured. Each
// fixture round-trips through oci.ParseConfig → oci.ImageConfig →
// manifestFromImageConfig → api.AppManifest; we assert the canonical
// AppManifest shape per fixture.

type fixtureCase struct {
	name       string
	fixture    string // path under testdata/oci-fixtures/
	wantArgv   []string
	wantUser   string
	wantHealth bool
	wantSignal string
}

func TestManifestFromImageConfig_Fixtures(t *testing.T) {
	t.Parallel()
	cases := []fixtureCase{
		{
			name:     "alpine_3_19",
			fixture:  "alpine_3_19.json",
			wantArgv: []string{"/bin/sh"},
		},
		{
			name:     "distroless_static",
			fixture:  "distroless_static.json",
			wantArgv: []string{"/app", "run"},
			wantUser: "65532",
		},
		{
			name:     "distroless_base",
			fixture:  "distroless_base.json",
			wantArgv: []string{"/bin/sh"},
			wantUser: "0",
		},
		{
			name:     "debian_slim_12",
			fixture:  "debian_slim_12.json",
			wantArgv: []string{"/bin/sh"},
		},
		{
			name:     "busybox_1_36",
			fixture:  "busybox_1_36.json",
			wantArgv: []string{"sh"},
		},
		{
			name:     "node_22_alpine",
			fixture:  "node_22_alpine.json",
			wantArgv: []string{"/docker-entrypoint.sh", "node", "index.js"},
			wantUser: "1001",
		},
		{
			name:     "python_3_12_slim",
			fixture:  "python_3_12_slim.json",
			wantArgv: []string{"python3", "main.py"},
			wantUser: "app", // USER 1000 normalises to DefaultAppUser
		},
		{
			name:       "healthcheck_full",
			fixture:    "healthcheck_full.json",
			wantArgv:   []string{"/app/server"},
			wantUser:   "1001",
			wantHealth: true,
			wantSignal: "SIGUSR1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadOCIFixture(t, tc.fixture)
			// Drive through the full registry path: ParseConfig
			// produces oci.Config; ManifestFromConfig produces
			// the api.AppManifest. This is the canonical
			// round-trip every commit 5 path runs.
			manifest, err := oci.ManifestFromConfig(cfg)
			if err != nil {
				t.Fatalf("ManifestFromConfig: %v", err)
			}
			if !stringSliceEq(manifest.Entrypoint, tc.wantArgv) {
				t.Errorf("Entrypoint = %v; want %v", manifest.Entrypoint, tc.wantArgv)
			}
			if tc.wantUser != "" && manifest.User != tc.wantUser {
				t.Errorf("User = %q; want %q", manifest.User, tc.wantUser)
			}
			if tc.wantHealth && manifest.Healthcheck == nil {
				t.Error("Healthcheck nil; want populated")
			}
			if tc.wantSignal != "" && manifest.StopSignal != tc.wantSignal {
				t.Errorf("StopSignal = %q; want %q", manifest.StopSignal, tc.wantSignal)
			}
		})
	}
}

// loadOCIFixture reads an OCI image-config JSON fixture and decodes
// it through oci.ParseConfig — the same path registry pulls take.
func loadOCIFixture(t *testing.T, name string) oci.Config {
	t.Helper()
	b, err := os.ReadFile("testdata/oci-fixtures/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	cfg, err := oci.ParseConfig(bytesReader(b))
	if err != nil {
		t.Fatalf("ParseConfig %s: %v", name, err)
	}
	return cfg
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func stringSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
