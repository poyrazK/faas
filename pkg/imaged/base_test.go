// Wave 0 stateless-only deny-list tests for pkg/imaged/base.go.
//
// The deny-list is the platform's first line of defense against stateful
// base images — postgres:16, redis:7, mysql:8, mongo:7 — that would
// silently lose data on the next wake/park cycle. These tests pin the
// ref-parsing predicate (firstPathSegment) and the deny-list membership
// check (StatefulDenyListMatch) so a future refactor (e.g. moving to a
// dedicated OCI ref parser) can't silently regress one branch.

package imaged

import (
	"strings"
	"testing"
)

// TestStatefulDenyListMatch_KnownStateful: every well-known stateful
// base image is denied regardless of registry, tag, or digest format.
func TestStatefulDenyListMatch_KnownStateful(t *testing.T) {
	cases := []string{
		"postgres",                      // bare Docker Hub short-form
		"postgres:16",                   // bare + tag
		"postgres:16-alpine",            // bare + tag
		"library/postgres",              // explicit Docker Hub library path
		"docker.io/library/postgres:16", // full Docker Hub ref
		"docker.io/postgres:16",         // Docker Hub short-form with registry
		"redis",
		"redis:7-alpine",
		"mysql:8.0",
		"mariadb:11",
		"mongo:7",
		"cockroach:v23.1",
		"cassandra:5.0",
		"clickhouse:24.1",
		"localhost:5000/myrepo/postgres:dev", // port + path + stateful image
		"127.0.0.1:5000/myrepo/postgres:dev", // IP-literal registry (no DNS name)
		"myreg.example.com/x/y/postgres:tag", // registry + nested path + stateful
		"postgres@sha256:0000000000000000000000000000000000000000000000000000000000000000", // digest-pinned
	}
	for _, ref := range cases {
		t.Run(ref, func(t *testing.T) {
			hint, denied := StatefulDenyListMatch(ref)
			if !denied {
				t.Errorf("expected denial for %q, got pass-through", ref)
			}
			if hint == "" {
				t.Errorf("expected non-empty hint for %q", ref)
			}
		})
	}
}

// TestStatefulDenyListMatch_KnownClean: a non-stateful image name is
// not denied even when it shares a substring with a denied name
// ("postgres-fork" must NOT match "postgres" because the first path
// segment is the full directory name, not a substring match).
func TestStatefulDenyListMatch_KnownClean(t *testing.T) {
	cases := []string{
		"ghcr.io/poyrazk/runner-node22:latest", // platform's own base
		"ghcr.io/poyrazk/runner-python312:latest",
		"node:22-slim",                 // not in the deny-list
		"ghcr.io/me/postgres-fork:1.0", // postgres-fork is NOT postgres
		"my-postgres-app",              // hyphenated name does not match "postgres"
		"alpine:3.20",
		"nginx:1.27",
		"python:3.12-slim",
		"", // empty ref → fail-open
		"docker.io/library/alpine:latest",
	}
	for _, ref := range cases {
		t.Run(ref, func(t *testing.T) {
			hint, denied := StatefulDenyListMatch(ref)
			if denied {
				t.Errorf("expected pass-through for %q, got denial (hint=%q)", ref, hint)
			}
		})
	}
}

// TestStatefulDenyListMatch_DigestForm: digest-pinned forms (the
// production default — issue #53 / M5 acceptance) still match when the
// image name is stateful. Pinned because the IndexAny(@, :) strip in
// firstPathSegment handles both formats and either branch regressing
// would silently let a stateful image through.
func TestStatefulDenyListMatch_DigestForm(t *testing.T) {
	cases := map[string]bool{
		"postgres@sha256:0000000000000000000000000000000000000000000000000000000000000000": true,
		"redis@sha256:0000000000000000000000000000000000000000000000000000000000000000":    true,
		"node@sha256:0000000000000000000000000000000000000000000000000000000000000000":     false,
	}
	for ref, wantDenied := range cases {
		t.Run(ref, func(t *testing.T) {
			_, denied := StatefulDenyListMatch(ref)
			if denied != wantDenied {
				t.Errorf("ref=%q denied=%v want=%v", ref, denied, wantDenied)
			}
		})
	}
}

// TestPathSegmentsAfterRegistry_EdgeCases: the underlying ref parser
// pins its behaviour on every shape we care about. Kept separate so a
// future refactor that splits the parser from the deny-list matcher
// (e.g. to expose it for tests in pkg/oci) has a clear acceptance test.
func TestPathSegmentsAfterRegistry_EdgeCases(t *testing.T) {
	cases := map[string][]string{
		"postgres":                           {"postgres"},
		"postgres:16":                        {"postgres"},
		"postgres@sha256:deadbeef":           {"postgres"},
		"library/postgres":                   {"library", "postgres"},
		"docker.io/library/postgres:16":      {"library", "postgres"},
		"docker.io/postgres:16":              {"postgres"},
		"ghcr.io/me/myapp:abc1234":           {"me", "myapp"},
		"localhost:5000/myrepo/myapp":        {"myrepo", "myapp"},
		"127.0.0.1:5000/myrepo/postgres:dev": {"myrepo", "postgres"},
		"myreg.example.com/x/y/z:tag":        {"x", "y", "z"},
		"docker.io/":                         nil, // registry-only, empty path
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := pathSegmentsAfterRegistry(in)
			if len(got) != len(want) {
				t.Fatalf("pathSegmentsAfterRegistry(%q) = %v, want %v", in, got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("pathSegmentsAfterRegistry(%q)[%d] = %q, want %q", in, i, got[i], want[i])
				}
			}
		})
	}
}

// TestStatefulBaseImageDenylist_AllKeysHaveHint: every entry in the
// deny-list MUST carry a remediation hint so the CLI can render an
// actionable message. Pinned because an empty hint silently degrades
// the customer experience to "stateless_only_violation" with no next
// step.
func TestStatefulBaseImageDenylist_AllKeysHaveHint(t *testing.T) {
	for name, hint := range StatefulBaseImageDenylist {
		if strings.TrimSpace(hint) == "" {
			t.Errorf("deny-list entry %q has empty hint", name)
		}
	}
}

// TestResolveDeployBaseRef_PerRuntimeEnvOverride is the regression for
// the function-deploy side of the EX44 crash loop (run 30661487390 +
// 30659586727 + 30656504195 — every BaseRef* const in pkg/imaged/base.go
// points at the unreachable ghcr.io/onebox-faas/... private namespace).
//
// baseRefFor (the pure const switch) returns the unreachable const for
// every runtime. Without resolveDeployBaseRef, the only operator
// override was FAAS_DEPLOY_BASE_REF (single global, wired at
// cmd/imaged/main.go:255) — wrong granularity for the box, which needs
// per-runtime digests. The helper walks DefaultRuntimeBaseRefs and
// applies the SAME per-runtime env-override + digest-pin posture that
// EnsureBases uses for the startup auto-stage path.
//
// Companion to TestResolveParentRef_HonorsEnvOverride in
// base_stage_test.go (startup path). Together they pin the full set
// of per-runtime env-override consumers.
func TestResolveDeployBaseRef_PerRuntimeEnvOverride(t *testing.T) {
	const overrideRef = "mirror.gcr.io/library/node@sha256:81d93757457f988523814ae0009837ae893f38d3fe123f2c37896f118b4c7804"
	const parentRef = "mirror.gcr.io/library/debian@sha256:81d93757457f988523814ae0009837ae893f38d3fe123f2c37896f118b4c7804"
	envLookup := func(key string) string {
		switch key {
		case "FAAS_DEPLOY_BASE_REF_NODE22":
			return overrideRef
		case "FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT":
			return parentRef
		}
		return ""
	}
	t.Run("env override present returns override", func(t *testing.T) {
		got, err := resolveDeployBaseRef(RuntimeNode22, envLookup)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != overrideRef {
			t.Errorf("got %q, want %q (env override must propagate to deploy-time base ref)", got, overrideRef)
		}
	})
	t.Run("env override missing returns const", func(t *testing.T) {
		got, err := resolveDeployBaseRef(RuntimeNode22, func(string) string { return "" })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != BaseRefNode22 {
			t.Errorf("got %q, want %q (missing env must not clobber const)", got, BaseRefNode22)
		}
	})
	t.Run("env override tag-only ref fails loud", func(t *testing.T) {
		tagOnly := func(key string) string {
			if key == "FAAS_DEPLOY_BASE_REF_NODE22" {
				return "mirror.gcr.io/library/node:22-alpine"
			}
			return ""
		}
		_, err := resolveDeployBaseRef(RuntimeNode22, tagOnly)
		if err == nil {
			t.Fatal("expected error for tag-only env override, got nil")
		}
		if !strings.Contains(err.Error(), "FAAS_DEPLOY_BASE_REF_NODE22") {
			t.Errorf("error should name the env var; got %v", err)
		}
	})
	t.Run("runtime not in table falls through to baseRefFor", func(t *testing.T) {
		// Customer-uploaded image with an unrecognised runtime —
		// must NOT crash, must NOT return empty. The baseRefFor
		// default (BaseRefMinimal) is the correct fallback.
		got, err := resolveDeployBaseRef("ruby33", func(string) string { return "" })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != BaseRefMinimal {
			t.Errorf("got %q, want %q (fallback to baseRefFor default)", got, BaseRefMinimal)
		}
	})
	t.Run("base-debian-parent runtime overrides correctly", func(t *testing.T) {
		// The parent runtime also lives in DefaultRuntimeBaseRefs,
		// so its env override must propagate too. Covers the
		// edge case where a customer runs the parent runtime
		// directly (debug / e2e tests).
		got, err := resolveDeployBaseRef(RuntimeDebianParent, envLookup)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != parentRef {
			t.Errorf("got %q, want %q (parent runtime env override)", got, parentRef)
		}
	})
	t.Run("nil envLookup falls back to os.Getenv without panic", func(t *testing.T) {
		// Defensive: nil envLookup must not crash. With os.Getenv
		// reading the empty test environment, the helper returns
		// the const for any runtime in the table.
		// (Set FAAS_DEPLOY_BASE_REF_NODE22 to mirror.gcr.io
		// upstream if a future fixture wants to exercise the
		// non-empty branch through os.Getenv.)
		got, err := resolveDeployBaseRef(RuntimeNode22, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != BaseRefNode22 {
			t.Errorf("got %q, want %q (nil envLookup should read os.Getenv)", got, BaseRefNode22)
		}
	})
}

func TestResolveDeployBaseRef_NamedNodeRequiresImmutableDefault(t *testing.T) {
	envLookup := func(key string) string {
		if key == "FAAS_NODE_NAME" {
			return "fsn-2.faas"
		}
		return ""
	}
	if _, err := resolveDeployBaseRef(RuntimeNode24, envLookup); err == nil || !strings.Contains(err.Error(), "digest-pinned") {
		t.Fatalf("named node default = %v, want digest-pin error", err)
	}
}

func TestResolveDeployBaseRef_MinimalOverride(t *testing.T) {
	const ref = "ghcr.io/poyrazk/base-minimal@sha256:81d93757457f988523814ae0009837ae893f38d3fe123f2c37896f118b4c7804"
	envLookup := func(key string) string {
		if key == "FAAS_DEPLOY_BASE_REF_MINIMAL" {
			return ref
		}
		return ""
	}
	got, err := resolveDeployBaseRef("", envLookup)
	if err != nil || got != ref {
		t.Fatalf("minimal ref = (%q, %v), want (%q, nil)", got, err, ref)
	}
}

func TestResolveDeployBaseRef_RetiresGlobalAndAllowsTestRedirect(t *testing.T) {
	if _, err := resolveDeployBaseRef(RuntimeNode22, func(key string) string {
		if key == "FAAS_DEPLOY_BASE_REF" {
			return "registry.example/onebox/runtime:latest"
		}
		return ""
	}); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("global override error = %v, want retired error", err)
	}
	testRef := "127.0.0.1:5000/onebox/deploy-base:latest"
	got, err := resolveDeployBaseRef(RuntimeNode22, func(key string) string {
		if key == testDeployBaseRefEnv {
			return testRef
		}
		return ""
	})
	if err != nil || got != testRef {
		t.Fatalf("test redirect = (%q, %v), want (%q, nil)", got, err, testRef)
	}
	if _, err := resolveDeployBaseRef(RuntimeNode22, func(key string) string {
		if key == testDeployBaseRefEnv {
			return testRef
		}
		if key == "FAAS_NODE_NAME" {
			return "compute-1"
		}
		return ""
	}); err == nil || !strings.Contains(err.Error(), "test-only") {
		t.Fatalf("named-node test redirect error = %v, want test-only error", err)
	}
}
