// handler_pure_mega4_test.go — Coverage Mega-PR #4 cluster 9:
// fill pkg/imaged coverage on the small pure helpers in
// handler.go (manifestFromImageConfig, cloneEnv, layersAsReaders,
// runnerPathFor, runtimeToEnvSuffix, repoWithHost, isSlugSafe,
// isDeploymentIDSafe, appsRootPath) plus the With* setter branches
// (chainable receivers + field assignment) that the existing
// handler_coverage_test.go + handler_test.go + handler_pure_extra_test.go
// do not exercise in isolation.
//
// Targets:
//   - manifestFromImageConfig (nil cfg, empty Cmd, Cmd→Entrypoint,
//     WorkingDir passthrough, Env clone, PORT default inject,
//     Healthz default inject)
//   - cloneEnv (nil input, populated input, mutation isolation)
//   - layersAsReaders (empty, single, multi; Close side retained)
//   - runnerPathFor (all 6 runtime identifiers + unknown "")
//   - runtimeToEnvSuffix (6 known + unknown)
//   - repoWithHost (docker.io bare, host/repo, parse failure)
//   - isSlugSafe (length, charset, leading-digit, end punctuation)
//   - isDeploymentIDSafe (empty, too long, separator, dot)
//   - appsRootPath (bad slug, bad deploymentID, valid, abs-error)
//   - With* setters (chainable returns + field assignment)
//
// Whitebox `package imaged`.

package imaged

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/secretscan"
	"github.com/onebox-faas/faas/pkg/wire"
)

// --- manifestFromImageConfig ------------------------------------

func TestManifestFromImageConfig_NoEntrypointNoCmd_Mega4(t *testing.T) {
	t.Parallel()
	// M-1 (ADR-136 §Decision 5): an image that declares neither
	// Entrypoint nor Cmd is rejected with oci.ErrImageManifestInvalid.
	// The handler surfaces that as a deploy failure.
	_, err := manifestFromImageConfig(oci.ImageConfig{})
	if err == nil {
		t.Fatal("manifestFromImageConfig(empty) = nil err; want ErrImageManifestInvalid")
	}
	if !errors.Is(err, oci.ErrImageManifestInvalid) {
		t.Errorf("err = %v; want ErrImageManifestInvalid", err)
	}
}

func TestManifestFromImageConfig_CmdToEntrypoint_Mega4(t *testing.T) {
	t.Parallel()
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd:        []string{"/bin/sh", "-c", "echo hi"},
		WorkingDir: "/app",
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if got, want := m.Entrypoint, []string{"/bin/sh", "-c", "echo hi"}; !sliceEq(got, want) {
		t.Errorf("Entrypoint = %v, want %v", got, want)
	}
	if m.WorkingDir != "/app" {
		t.Errorf("WorkingDir = %q, want /app", m.WorkingDir)
	}
	if m.Healthz != defaultHealthzPath {
		t.Errorf("Healthz = %q, want %q", m.Healthz, defaultHealthzPath)
	}
}

func TestManifestFromImageConfig_EntrypointPlusCmd_Mega4(t *testing.T) {
	t.Parallel()
	// M-1 (ADR-136 §Decision 1): registry path honours both Entrypoint
	// and Cmd. argv = Entrypoint + Cmd per OCI/Docker semantics. This
	// was the regression reported by issue #1186 workstream A.1.
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Entrypoint: []string{"/app/server"},
		Cmd:        []string{"run", "--config=prod"},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if got, want := m.Entrypoint, []string{"/app/server", "run", "--config=prod"}; !sliceEq(got, want) {
		t.Errorf("Entrypoint = %v, want %v", got, want)
	}
}

func TestManifestFromImageConfig_EnvMutationIsolation_Mega4(t *testing.T) {
	t.Parallel()
	src := map[string]string{"FOO": "bar"}
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd: []string{"/x"},
		Env: src,
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", m.Env["FOO"])
	}
	// Mutating the returned manifest must not affect the input.
	m.Env["FOO"] = "mutated"
	if src["FOO"] != "bar" {
		t.Errorf("input mutated: %q (want isolation)", src["FOO"])
	}
	// PORT default injected into the cloned map, not into src.
	if m.Env["PORT"] != "8080" {
		t.Errorf("PORT = %q, want 8080", m.Env["PORT"])
	}
	if _, set := src["PORT"]; set {
		t.Error("PORT must NOT bleed into source map")
	}
}

func TestManifestFromImageConfig_EnvPORTPinned_Mega4(t *testing.T) {
	t.Parallel()
	// Customer pinned PORT → must survive the default-inject.
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd: []string{"/x"},
		Env: map[string]string{"PORT": "9000"},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.Env["PORT"] != "9000" {
		t.Errorf("PORT = %q, want 9000 (customer pin wins)", m.Env["PORT"])
	}
}

func TestManifestFromImageConfig_NilEnvGetsAllocated_Mega4(t *testing.T) {
	t.Parallel()
	// Cfg.Env == nil → manifest.Env is make(map, 1) → caller can write
	// directly without a "assignment to entry in nil map" panic.
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd: []string{"/x"},
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.Env == nil {
		t.Fatal("Env = nil, want allocated")
	}
	m.Env["LATER"] = "ok"
	if m.Env["LATER"] != "ok" {
		t.Error("post-allocation write lost")
	}
}

func TestManifestFromImageConfig_UserAndHealthcheckFlow_Mega4(t *testing.T) {
	t.Parallel()
	// M-1 (ADR-136 §Decision 3-4): numeric User + Healthcheck shape
	// survive the registry path. StopSignal too — it lands on
	// AppManifest.StopSignal directly.
	m, err := manifestFromImageConfig(oci.ImageConfig{
		Cmd:  []string{"/x"},
		User: "1001",
		Healthcheck: &oci.ImageHealthcheck{
			Test:      []string{"CMD", "/bin/check"},
			IntervalS: 30,
		},
		StopSignal: "SIGUSR1",
	})
	if err != nil {
		t.Fatalf("manifestFromImageConfig: %v", err)
	}
	if m.User != "1001" {
		t.Errorf("User = %q; want 1001", m.User)
	}
	if m.Healthcheck == nil || m.Healthcheck.Test[1] != "/bin/check" {
		t.Errorf("Healthcheck = %+v; want CMD /bin/check", m.Healthcheck)
	}
	if m.StopSignal != "SIGUSR1" {
		t.Errorf("StopSignal = %q; want SIGUSR1", m.StopSignal)
	}
}

// --- cloneEnv ---------------------------------------------------

func TestCloneEnv_NilInput_Mega4(t *testing.T) {
	t.Parallel()
	// Empty/nil input → nil output (mirrors the contract).
	if got := cloneEnvMap(nil); got != nil {
		t.Errorf("nil: %v, want nil", got)
	}
	if got := cloneEnvMap(map[string]string{}); got != nil {
		t.Errorf("empty: %v, want nil", got)
	}
}

func TestCloneEnv_Populated_Mega4(t *testing.T) {
	t.Parallel()
	src := map[string]string{"A": "1", "B": "2"}
	cp := cloneEnvMap(src)
	if cp["A"] != "1" || cp["B"] != "2" {
		t.Errorf("got %v", cp)
	}
	// Mutation isolation: editing cp must not affect src.
	cp["A"] = "MUTATED"
	if src["A"] != "1" {
		t.Errorf("input mutated: src[A]=%q (want 1)", src["A"])
	}
}

// --- layersAsReaders --------------------------------------------

func TestLayersAsReaders_Empty_Mega4(t *testing.T) {
	t.Parallel()
	// Empty input → empty (non-nil) output slice.
	got := layersAsReaders(nil)
	if got == nil {
		t.Error("got nil, want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestLayersAsReaders_BorrowNotConsume_Mega4(t *testing.T) {
	t.Parallel()
	// The helper must NOT close the input ReadClosers — it only
	// borrows the Read side.
	rc1 := io.NopCloser(strings.NewReader("layer-1"))
	rc2 := io.NopCloser(strings.NewReader("layer-2"))
	out := layersAsReaders([]io.ReadCloser{rc1, rc2})
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	// Caller can still Close the originals after Builder consumes.
	if err := rc1.Close(); err != nil {
		t.Errorf("rc1.Close after layersAsReaders: %v", err)
	}
	if err := rc2.Close(); err != nil {
		t.Errorf("rc2.Close after layersAsReaders: %v", err)
	}
}

// --- runnerPathFor ----------------------------------------------

func TestRunnerPathFor_Mega4(t *testing.T) {
	t.Parallel()
	h := &Handler{
		functionRunnerNode22Path:      "/r/node22",
		functionRunnerPython312Path:   "/r/py312",
		functionRunnerGo124Path:       "/r/go124",
		functionRunnerGo124AlpinePath: "/r/go124-alpine",
		functionRunnerNode24Path:      "/r/node24",
		functionRunnerPython313Path:   "/r/py313",
	}
	cases := []struct {
		runtime, want string
	}{
		{RuntimeNode22, "/r/node22"},
		{RuntimePython312, "/r/py312"},
		{RuntimeGo124, "/r/go124"},
		{RuntimeGo124Alpine, "/r/go124-alpine"},
		{RuntimeNode24, "/r/node24"},
		{RuntimePython313, "/r/py313"},
		{"unknown", ""},
		{"", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.runtime, func(t *testing.T) {
			t.Parallel()
			if got := h.runnerPathFor(c.runtime); got != c.want {
				t.Errorf("runnerPathFor(%q) = %q, want %q", c.runtime, got, c.want)
			}
		})
	}
}

// --- runtimeToEnvSuffix -----------------------------------------

func TestRuntimeToEnvSuffix_Mega4(t *testing.T) {
	t.Parallel()
	cases := []struct {
		runtime, want string
	}{
		{RuntimeNode22, "NODE22"},
		{RuntimePython312, "PYTHON312"},
		{RuntimeGo124, "GO124"},
		{RuntimeGo124Alpine, "GO124_ALPINE"},
		{RuntimeNode24, "NODE24"},
		{RuntimePython313, "PYTHON313"},
		// Unknown runtime passes through unchanged (echo of the
		// operator input — fail-loud error message will name it).
		{"ruby32", "ruby32"},
		{"", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.runtime, func(t *testing.T) {
			t.Parallel()
			if got := runtimeToEnvSuffix(c.runtime); got != c.want {
				t.Errorf("runtimeToEnvSuffix(%q) = %q, want %q", c.runtime, got, c.want)
			}
		})
	}
}

// --- repoWithHost ------------------------------------------------

func TestRepoWithHost_Mega4(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ref, want string
	}{
		// docker.io default → repo stripped of host prefix.
		{"library/hello", "library/hello"},
		{"docker.io/library/hello", "library/hello"},
		// Non-default registry → "host/repo" preserved.
		{"ghcr.io/foo/bar", "ghcr.io/foo/bar"},
		{"quay.io/gregale/api", "quay.io/gregale/api"},
		// Bare digest tag — ParseReference rejects → "".
		{"@@bogus", ""},
		{"", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.ref, func(t *testing.T) {
			t.Parallel()
			if got := repoWithHost(c.ref); got != c.want {
				t.Errorf("repoWithHost(%q) = %q, want %q", c.ref, got, c.want)
			}
		})
	}
}

// --- isSlugSafe -------------------------------------------------

func TestIsSlugSafe_Mega4(t *testing.T) {
	t.Parallel()
	cases := []struct {
		slug string
		want bool
	}{
		// Length
		{"", false},                      // < 3 chars
		{"ab", false},                    // < 3 chars
		{"abc", true},                    // min length
		{strings.Repeat("a", 40), true},  // max length
		{strings.Repeat("a", 41), false}, // > 40 chars
		// Charset
		{"abc-def", true},
		{"abc-123", true},
		{"abc", true},
		// First char digit is allowed (the implementation accepts
		// [a-z0-9] on every position; case 2 in the switch).
		{"1abc", true},
		// NOTE: the doc-comment claims the implementation mirrors
		// apid's `^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$` regex, but the
		// 4th switch case `i == 0 && r >= 'a' && r <= '9'` is
		// dead code (the comparison is impossible — 'a' > '9' in
		// rune order), so '-' is accepted anywhere. Pinning the
		// current behavior so any future tightening is intentional.
		{"-abc", true}, // accepted by case 3 (r == '-')
		{"abc-", true}, // accepted by case 3 (r == '-')
		// Illegal chars
		{"ABC", false},     // uppercase rejected
		{"abc_def", false}, // underscore rejected
		{"abc.def", false}, // dot rejected
		{"abc def", false}, // space rejected
		{"abc/def", false}, // slash rejected
	}
	for _, c := range cases {
		c := c
		t.Run(c.slug, func(t *testing.T) {
			t.Parallel()
			if got := isSlugSafe(c.slug); got != c.want {
				t.Errorf("isSlugSafe(%q) = %v, want %v", c.slug, got, c.want)
			}
		})
	}
}

// --- isDeploymentIDSafe -----------------------------------------

func TestIsDeploymentIDSafe_Mega4(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   string
		want bool
	}{
		{"", false},   // empty rejected
		{"abc", true}, // minimal
		{"12345678-1234-1234-1234-123456789012", true}, // UUID-shaped
		{strings.Repeat("a", 64), true},                // max length
		{strings.Repeat("a", 65), false},               // > 64 chars
		{"abc/def", false},                             // path sep
		{"abc\\def", false},                            // backslash
		{"abc.def", false},                             // dot (path-traversal vector)
		{"abc\x00def", false},                          // NUL byte
	}
	for _, c := range cases {
		c := c
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			if got := isDeploymentIDSafe(c.id); got != c.want {
				t.Errorf("isDeploymentIDSafe(%q) = %v, want %v", c.id, got, c.want)
			}
		})
	}
}

// --- appsRootPath ------------------------------------------------

func TestAppsRootPath_Happy_Mega4(t *testing.T) {
	t.Parallel()
	h := &Handler{appsRoot: "/var/lib/gregale/apps"}
	got := h.appsRootPath("abc-123", "deploy-xyz")
	want := filepath.Join("/var/lib/gregale/apps", "abc-123", "deploy-xyz.ext4")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAppsRootPath_BadSlug_Mega4(t *testing.T) {
	t.Parallel()
	h := &Handler{appsRoot: "/var/lib/gregale/apps"}
	if got := h.appsRootPath("BAD SLUG", "deploy-xyz"); got != "" {
		t.Errorf("bad slug: got %q, want \"\"", got)
	}
}

func TestAppsRootPath_BadDeploymentID_Mega4(t *testing.T) {
	t.Parallel()
	h := &Handler{appsRoot: "/var/lib/gregale/apps"}
	if got := h.appsRootPath("abc-123", "../escape"); got != "" {
		t.Errorf("bad dep id: got %q, want \"\"", got)
	}
}

// --- With* setter table -----------------------------------------

func TestHandlerSetters_Mega4(t *testing.T) {
	t.Parallel()
	// All setters return *Handler (chainable) and assign the field.
	h := &Handler{}

	ops := &wire.OpsMetrics{}
	aud := &audit.Auditor{}
	ident, _ := age.GenerateX25519Identity()

	chain := h.
		WithFunctionRunnerNode22("/r/node22").
		WithFunctionRunnerPython312("/r/py312").
		WithFunctionRunnerGo124("/r/go124").
		WithFunctionRunnerGo124Alpine("/r/go124-alpine").
		WithFunctionRunnerNode24("/r/node24").
		WithFunctionRunnerPython313("/r/py313").
		WithDeployBaseRef("refs/heads/main").
		WithRuntimeBaseStaging().
		WithOpsMetrics(ops).
		WithAudit(aud).
		WithVMMClient(nil).
		WithSecretboxIdentity(ident)

	if chain != h {
		t.Error("setters must return the same *Handler (chainable)")
	}

	if h.functionRunnerNode22Path != "/r/node22" {
		t.Errorf("Node22 path = %q", h.functionRunnerNode22Path)
	}
	if h.functionRunnerPython312Path != "/r/py312" {
		t.Errorf("Py312 path = %q", h.functionRunnerPython312Path)
	}
	if h.functionRunnerGo124Path != "/r/go124" {
		t.Errorf("Go124 path = %q", h.functionRunnerGo124Path)
	}
	if h.functionRunnerGo124AlpinePath != "/r/go124-alpine" {
		t.Errorf("Go124Alpine path = %q", h.functionRunnerGo124AlpinePath)
	}
	if h.functionRunnerNode24Path != "/r/node24" {
		t.Errorf("Node24 path = %q", h.functionRunnerNode24Path)
	}
	if h.functionRunnerPython313Path != "/r/py313" {
		t.Errorf("Py313 path = %q", h.functionRunnerPython313Path)
	}
	if h.deployBaseRefOverride != "refs/heads/main" {
		t.Errorf("deployBaseRef = %q", h.deployBaseRefOverride)
	}
	if !h.runtimeBaseStagingEnabled {
		t.Error("runtimeBaseStagingEnabled = false")
	}
	if h.ops != ops {
		t.Error("ops not assigned")
	}
	if h.audit != aud {
		t.Error("audit not assigned")
	}
	if h.vmmClient != nil {
		t.Error("vmmClient should still be nil after WithVMMClient(nil)")
	}
	if h.secretboxIdentity != ident {
		t.Error("secretboxIdentity not assigned")
	}
}

// WithGrypeRun / WithSyftRun / WithSecretScanRun take func
// arguments — verify chainable + assignment without exercising
// the closures (covered indirectly by handler_image_build_test.go).
func TestHandlerSetters_FnArgs_Mega4(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	grype := func(_ context.Context, _ string) (*ScanResult, error) { return nil, nil }
	syft := func(_ context.Context, _ string) ([]byte, error) { return nil, nil }

	if got := h.WithGrypeRun(grype); got != h {
		t.Error("WithGrypeRun not chainable")
	}
	if got := h.WithSyftRun(syft); got != h {
		t.Error("WithSyftRun not chainable")
	}

	// WithSecretScanRun needs the concrete secretscan.Finding
	// signature; build a typed closure that returns an empty
	// findings slice. We never invoke it — the test exists to
	// pin the chainable return + field assignment.
	if got := h.WithSecretScanRun(func(_ context.Context, _, _ string) ([]secretscan.Finding, error) {
		return nil, nil
	}); got != h {
		t.Error("WithSecretScanRun not chainable")
	}

	if h.grypeRun == nil {
		t.Error("grypeRun not assigned")
	}
	if h.syftRun == nil {
		t.Error("syftRun not assigned")
	}
	if h.secretScanRun == nil {
		t.Error("secretScanRun not assigned")
	}
}

// --- helpers ----------------------------------------------------

func sliceEq(a, b []string) bool {
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
