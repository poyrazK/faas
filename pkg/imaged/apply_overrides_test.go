package imaged

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestApplyOverrides pins the pure-function behaviour of applyOverrides for
// every row of the behaviour table in the PR-B plan. Each case is a
// self-contained table entry so a regression in one row surfaces by name.
//
// Mirrors the canonical imaged-layer test style (no harness, no fixtures):
// the helper takes (manifest, dep) and returns (manifest, error); tests
// construct both directly. The empty-override case is the no-op regression
// net so a future refactor cannot accidentally start writing on a dep with
// no overrides.
func TestApplyOverrides(t *testing.T) {
	tests := []struct {
		name    string
		base    api.AppManifest
		dep     state.Deployment
		want    api.AppManifest
		wantErr bool
	}{
		// --- entrypoint + cmd ---

		{
			name: "no override preserves OCI argv",
			base: api.AppManifest{Entrypoint: []string{"node", "server.js"}, Env: map[string]string{"OCI": "yes"}},
			dep:  state.Deployment{},
			want: api.AppManifest{Entrypoint: []string{"node", "server.js"}, Env: map[string]string{"OCI": "yes"}},
		},
		{
			name: "override entrypoint replaces OCI argv",
			base: api.AppManifest{Entrypoint: []string{"node", "server.js"}},
			dep:  state.Deployment{OverrideEntrypoint: []string{"python312:app.handler"}},
			want: api.AppManifest{Entrypoint: []string{"python312:app.handler"}},
		},
		{
			name: "override cmd alone appends to OCI argv",
			base: api.AppManifest{Entrypoint: []string{"node", "server.js"}},
			dep:  state.Deployment{OverrideCmd: []string{"--port", "9090"}},
			want: api.AppManifest{Entrypoint: []string{"node", "server.js", "--port", "9090"}},
		},
		{
			name: "override entrypoint + cmd produces argv-with-args",
			base: api.AppManifest{Entrypoint: []string{"node", "server.js"}},
			dep: state.Deployment{
				OverrideEntrypoint: []string{"/usr/local/bin/custom"},
				OverrideCmd:        []string{"--port", "9090"},
			},
			want: api.AppManifest{Entrypoint: []string{"/usr/local/bin/custom", "--port", "9090"}},
		},

		// --- env (merge, override wins on collision) ---

		{
			name: "override env merges with non-colliding OCI keys",
			base: api.AppManifest{Env: map[string]string{"OCI_VAR": "from_image"}},
			dep:  state.Deployment{OverrideEnv: mustJSON(t, map[string]string{"LOG_LEVEL": "debug"})},
			want: api.AppManifest{Env: map[string]string{"OCI_VAR": "from_image", "LOG_LEVEL": "debug"}},
		},
		{
			name: "override env wins on key collision",
			base: api.AppManifest{Env: map[string]string{"LOG_LEVEL": "info"}},
			dep:  state.Deployment{OverrideEnv: mustJSON(t, map[string]string{"LOG_LEVEL": "debug"})},
			want: api.AppManifest{Env: map[string]string{"LOG_LEVEL": "debug"}},
		},
		{
			name: "override env alone initializes manifest env",
			base: api.AppManifest{},
			dep:  state.Deployment{OverrideEnv: mustJSON(t, map[string]string{"FOO": "bar"})},
			want: api.AppManifest{Env: map[string]string{"FOO": "bar"}},
		},

		// --- env_secrets (refs carry through onto manifest.EnvSecrets) ---

		{
			name: "override env_secrets populates manifest EnvSecrets",
			base: api.AppManifest{},
			dep: state.Deployment{
				OverrideEnvSecrets: mustJSON(t, map[string]string{"DB_URL": "secret:DB_URL"}),
			},
			want: api.AppManifest{EnvSecrets: map[string]string{"DB_URL": "secret:DB_URL"}},
		},
		{
			name: "env_secrets and env populate different fields",
			base: api.AppManifest{},
			dep: state.Deployment{
				OverrideEnv:        mustJSON(t, map[string]string{"LOG_LEVEL": "debug"}),
				OverrideEnvSecrets: mustJSON(t, map[string]string{"DB_URL": "secret:DB_URL"}),
			},
			want: api.AppManifest{
				Env:        map[string]string{"LOG_LEVEL": "debug"},
				EnvSecrets: map[string]string{"DB_URL": "secret:DB_URL"},
			},
		},

		// --- port (dormant today; sets manifest.Port for PR-C) ---

		{
			name: "override port sets manifest.Port",
			base: api.AppManifest{Entrypoint: []string{"x"}},
			dep:  state.Deployment{OverridePort: 9090},
			want: api.AppManifest{Entrypoint: []string{"x"}, Port: 9090},
		},
		{
			name: "no override port preserves manifest.Port",
			base: api.AppManifest{Entrypoint: []string{"x"}, Port: 3000},
			dep:  state.Deployment{},
			want: api.AppManifest{Entrypoint: []string{"x"}, Port: 3000},
		},

		// --- healthcheck (dormant today; sets manifest.Healthz for PR-C) ---

		{
			name: "override healthcheck path sets manifest.Healthz",
			base: api.AppManifest{Entrypoint: []string{"x"}},
			dep: state.Deployment{
				OverrideHealthcheck: mustJSON(t, api.DeploymentHealthcheck{
					Path:      "/readyz",
					IntervalS: 10,
					TimeoutS:  5,
					Retries:   3,
				}),
			},
			want: api.AppManifest{Entrypoint: []string{"x"}, Healthz: "/readyz"},
		},
		{
			name: "override healthcheck with empty path is no-op for Healthz",
			base: api.AppManifest{Entrypoint: []string{"x"}, Healthz: "/healthz"},
			dep: state.Deployment{
				OverrideHealthcheck: mustJSON(t, api.DeploymentHealthcheck{Path: ""}),
			},
			// Empty path preserved (does not overwrite existing Healthz).
			want: api.AppManifest{Entrypoint: []string{"x"}, Healthz: "/healthz"},
		},

		// --- triple overlap: every override field set simultaneously ---

		{
			name: "all six override fields set at once",
			base: api.AppManifest{
				Entrypoint: []string{"node", "server.js"},
				Env:        map[string]string{"LOG_LEVEL": "info"},
				Port:       8080,
				Healthz:    "/healthz",
			},
			dep: state.Deployment{
				OverrideEntrypoint:  []string{"/usr/local/bin/custom"},
				OverrideCmd:         []string{"--port", "9090"},
				OverrideEnv:         mustJSON(t, map[string]string{"LOG_LEVEL": "debug"}),
				OverrideEnvSecrets:  mustJSON(t, map[string]string{"DB_URL": "secret:DB_URL"}),
				OverridePort:        9090,
				OverrideHealthcheck: mustJSON(t, api.DeploymentHealthcheck{Path: "/readyz"}),
			},
			want: api.AppManifest{
				Entrypoint: []string{"/usr/local/bin/custom", "--port", "9090"},
				Env:        map[string]string{"LOG_LEVEL": "debug"},
				EnvSecrets: map[string]string{"DB_URL": "secret:DB_URL"},
				Port:       9090,
				Healthz:    "/readyz",
			},
		},

		// --- error path: malformed jsonb column ---

		{
			name:    "malformed override_env jsonb returns error",
			base:    api.AppManifest{Entrypoint: []string{"x"}},
			dep:     state.Deployment{OverrideEnv: json.RawMessage(`{"LOG_LEVEL": `)}, // truncated
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyOverrides(tt.base, tt.dep)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			// Compare Env / EnvSecrets as maps regardless of nil-vs-empty
			// (manifest contract treats them the same). Slices are compared
			// verbatim because entrypoint order is part of the contract.
			if !manifestsEqualIgnoreEmptyMaps(got, tt.want) {
				t.Errorf("manifest mismatch:\n got  = %+v\n want = %+v", got, tt.want)
			}
		})
	}
}

// TestApplyOverrides_DoesNotMutateInput pins the defensive-copy contract:
// applyOverrides returns a new manifest. The input is left untouched so a
// caller that re-runs the helper on the same base (e.g. a retry after a
// transient imaged error) does not double-apply overrides.
func TestApplyOverrides_DoesNotMutateInput(t *testing.T) {
	base := api.AppManifest{
		Entrypoint: []string{"node", "server.js"},
		Env:        map[string]string{"LOG_LEVEL": "info"},
	}
	dep := state.Deployment{
		OverrideEnv: mustJSON(t, map[string]string{"LOG_LEVEL": "debug"}),
	}
	out, err := applyOverrides(base, dep)
	if err != nil {
		t.Fatalf("applyOverrides: %v", err)
	}
	if out.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("output env LOG_LEVEL = %q, want debug", out.Env["LOG_LEVEL"])
	}
	if base.Env["LOG_LEVEL"] != "info" {
		t.Errorf("input env was mutated: LOG_LEVEL = %q, want info (unchanged)", base.Env["LOG_LEVEL"])
	}
	// Entry point — base argv unchanged, output argv is also unchanged here
	// (no OverrideEntrypoint/OverrideCmd in this test).
	if len(base.Entrypoint) != 2 || base.Entrypoint[0] != "node" {
		t.Errorf("input entrypoint mutated: %v", base.Entrypoint)
	}
}

// mustJSON marshals v as the json.RawMessage that a pgstore.GetDeployment
// round-trip would have produced (json.Marshal — no canonical indent).
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// manifestsEqualIgnoreEmptyMaps compares two AppManifest values treating a
// nil map and an empty map as equal. reflect.DeepEqual is strict about the
// distinction, which makes round-tripping manifest values through Write/Read
// noisy in unrelated tests. The contract-level invariant is "no keys
// present", not "the backing map allocation differs". nilIfEmpty below
// collapses both to nil; DeepEqual then sees the same shape.
func manifestsEqualIgnoreEmptyMaps(a, b api.AppManifest) bool {
	aClean := api.AppManifest{
		Entrypoint:       a.Entrypoint,
		Env:              nilIfEmpty(a.Env),
		EnvSecrets:       nilIfEmpty(a.EnvSecrets),
		WorkingDir:       a.WorkingDir,
		Port:             a.Port,
		Healthz:          a.Healthz,
		User:             a.User,
		ExecutionMode:    a.ExecutionMode,
		RestartPolicy:    a.RestartPolicy,
		StartupDeadlineS: a.StartupDeadlineS,
		MaxRetries:       a.MaxRetries,
		ServiceReplicas:  a.ServiceReplicas,
	}
	bClean := api.AppManifest{
		Entrypoint:       b.Entrypoint,
		Env:              nilIfEmpty(b.Env),
		EnvSecrets:       nilIfEmpty(b.EnvSecrets),
		WorkingDir:       b.WorkingDir,
		Port:             b.Port,
		Healthz:          b.Healthz,
		User:             b.User,
		ExecutionMode:    b.ExecutionMode,
		RestartPolicy:    b.RestartPolicy,
		StartupDeadlineS: b.StartupDeadlineS,
		MaxRetries:       b.MaxRetries,
		ServiceReplicas:  b.ServiceReplicas,
	}
	return reflect.DeepEqual(aClean, bClean)
}

func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

func TestApplyAppLifecycle(t *testing.T) {
	manifest := api.AppManifest{Entrypoint: []string{"/app/server"}, ExecutionMode: api.ExecutionModeWorker}
	app := state.App{Manifest: state.AppManifest{
		ExecutionMode:    api.ExecutionModeService,
		RestartPolicy:    api.RestartPolicyAlways,
		StartupDeadlineS: 30,
		MaxRetries:       5,
		ServiceReplicas:  &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2},
	}}
	got := applyAppLifecycle(manifest, app)
	if got.ExecutionMode != api.ExecutionModeService || got.RestartPolicy != api.RestartPolicyAlways ||
		got.StartupDeadlineS != 30 || got.MaxRetries != 5 || got.ServiceReplicas == nil ||
		got.ServiceReplicas.Desired != 2 {
		t.Fatalf("lifecycle overlay = %+v", got)
	}
	got.ServiceReplicas.Desired = 3
	if app.Manifest.ServiceReplicas.Desired != 2 {
		t.Fatal("lifecycle overlay retained the state pointer")
	}
}
