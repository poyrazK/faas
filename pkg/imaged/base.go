package imaged

import (
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/oci"
)

// Base image references per runtime (spec §4.6 two-drive scheme, ADR-005).
//
// imaged.handleDeployment pulls the app's manifest, then pulls the matching
// base's manifest to learn the base's diff_ids, then computes LayersAboveBase.
// The base itself is NOT downloaded — drive0 is the shared read-only ext4
// produced from the base image, already on disk at /srv/fc/base/<runtime>.ext4.
//
// The defaults below match images/runner-node22.Dockerfile,
// images/runner-python312.Dockerfile, and images/base-minimal.Dockerfile on
// HEAD of main. They can be overridden at startup via config (cmd/imaged's
// TOML) so the box can roll a base image ahead of pinned refs and have imaged
// track it without a code change.
const (
	// testDeployBaseRefEnv is intentionally separate from the production
	// per-runtime knobs. It is only forwarded by the hermetic e2e harness so
	// tests can point both builderd and imaged at a local registry.
	testDeployBaseRefEnv = "FAAS_TEST_DEPLOY_BASE_REF"

	BaseRefNode22      = "ghcr.io/poyrazk/runner-node22:latest"
	BaseRefPython312   = "ghcr.io/poyrazk/runner-python312:latest"
	BaseRefGo124       = "ghcr.io/poyrazk/runner-go124:latest"
	BaseRefGo124Alpine = "ghcr.io/poyrazk/runner-go124-alpine:latest"
	BaseRefNode24      = "ghcr.io/poyrazk/runner-node24:latest"
	BaseRefPython313   = "ghcr.io/poyrazk/runner-python313:latest"
	BaseRefMinimal     = "ghcr.io/poyrazk/base-minimal:latest"
	BaseRefBuilder     = "ghcr.io/poyrazk/builder-base:latest"
	// BaseRefDebianParent (ADR-053) is the staging-only parent
	// runtime — its ext4 carries the shared debian:12-slim userland
	// (~150 MB of libc/openssl/ca-certs/busybox) that the four
	// node/python runtime bases used to duplicate. The Dockerfile
	// (images/base-debian-parent.Dockerfile) is `FROM debian:12-slim`
	// DIRECTLY — not scratch + COPY — so the first OCI layer in the
	// parent's manifest is the literal debian:12-slim rootfs layer
	// and `oci.LayersAboveBase(parent.DiffIDs, child.DiffIDs)`
	// succeeds (the chain-composability invariant documented in
	// ADR-053).
	BaseRefDebianParent = "ghcr.io/poyrazk/base-debian-parent:latest"

	// Runtime names are the values stored on state.App.Runtime. They map
	// 1:1 to the runner shims in
	// guest/runners/{node22,python312,go124,node24,python313}.
	// go124-alpine reuses the go124 runner shim against a musl base
	// (images/runner-go124-alpine.Dockerfile); libc only differs.
	// node24 uses /app/node24.js (versioned); python313 stays on the
	// version-neutral /app/handler.py. Naming them as constants keeps
	// the baseRefFor switch and the production callers (cmd/imaged's
	// deploy path) in lockstep.
	RuntimeNode22       = "node22"
	RuntimePython312    = "python312"
	RuntimeGo124        = "go124"
	RuntimeGo124Alpine  = "go124-alpine"
	RuntimeNode24       = "node24"
	RuntimePython313    = "python313"
	RuntimeDebianParent = "base-debian-parent" // ADR-053: shared parent runtime id
)

// StatefulBaseImageDenylist is the Wave 0 / year-one set of OCI image
// names this platform refuses to deploy as the customer-facing base.
// The platform is stateless-only — the customer must use a managed
// service (Neon / Upstash / PlanetScale / MongoDB Atlas) for any
// stateful workload, and inject credentials via `faas secrets set`.
//
// Keys are the lowercased first path segment of the image name
// (`postgres`, `redis`, `mysql`, …). The matcher strips the registry
// hostname and tag before comparing, so `postgres:16`,
// `postgres:16-alpine`, and `library/postgres` all match; while
// `ghcr.io/me/postgres-fork` does NOT match (it has `postgres-fork`,
// not `postgres`, as the first path segment — see StatefulDenyListMatch
// for the exact predicate).
//
// Values are short human-readable remediation hints that show up in
// the RFC 7807 Detail field so the CLI can render actionable copy.
//
// Not in pkg/api/limits.go (which is numeric-only per the platform
// convention): this list is constant code at ~6 entries. If it grows
// past ~20 or needs per-plan control, move it then.
var StatefulBaseImageDenylist = map[string]string{
	"postgres":   "use Neon (https://neon.tech) or Supabase Postgres",
	"redis":      "use Upstash Redis (https://upstash.com)",
	"mysql":      "use PlanetScale (https://planetscale.com)",
	"mariadb":    "use PlanetScale (https://planetscale.com)",
	"mongo":      "use MongoDB Atlas (https://mongodb.com/atlas)",
	"cockroach":  "use CockroachDB Cloud (https://cockroachlabs.cloud)",
	"cassandra":  "use Astra DB (https://astra.datastax.com)",
	"clickhouse": "use ClickHouse Cloud (https://clickhouse.cloud)",
}

// StatefulDenyListMatch returns (hint, true) when any path segment of
// the resolved image name (after stripping the registry hostname and
// tag/digest) is in StatefulBaseImageDenylist, else ("", false).
//
// ref is the full OCI reference apid stored into dep.ImageDigest, e.g.
// `docker.io/library/postgres:16`, `ghcr.io/me/myapp:abc1234`,
// `postgres@sha256:…`, `localhost:5000/myrepo/postgres:dev`. The function:
//
//  1. Strips the registry hostname — the first slash-separated segment,
//     but ONLY if it looks like a hostname (contains `.` or `:`, or is
//     `localhost`). This is critical: a bare `:tag` (port-less) on
//     `localhost:5000/...` would otherwise be confused with the image
//     tag.
//  2. From the remaining path, strips any trailing `:tag` or `@sha256:…`.
//  3. Splits on `/` and lowercases every segment.
//  4. Returns the FIRST segment that matches a deny-list key.
//
// We scan every segment (not just the first) so Docker Hub's canonical
// `library/postgres:16` form is caught — `library` is the registry path
// prefix on docker.io, not a meaningful namespace. Bare `postgres:16`
// also matches because there's only one segment after the strip.
//
// Returns ("", false) on any parse failure — fail-open at the matcher
// so a malformed ref never blocks a deploy (the customer's other
// failures will still fire).
func StatefulDenyListMatch(ref string) (string, bool) {
	if ref == "" {
		return "", false
	}
	segments := pathSegmentsAfterRegistry(ref)
	for _, seg := range segments {
		hint, ok := StatefulBaseImageDenylist[strings.ToLower(seg)]
		if ok {
			return hint, true
		}
	}
	return "", false
}

// pathSegmentsAfterRegistry strips the registry hostname from an OCI
// image ref and returns the remaining path segments (post tag/digest
// strip). Examples:
//
//	docker.io/library/postgres:16      → ["library", "postgres"]
//	docker.io/postgres:16              → ["postgres"]
//	ghcr.io/me/myapp:abc1234           → ["me", "myapp"]
//	postgres:16                        → ["postgres"]
//	postgres@sha256:deadbeef           → ["postgres"]
//	localhost:5000/myrepo/postgres:dev → ["myrepo", "postgres"]
//	myreg.example.com/x/y/z:tag        → ["x", "y", "z"]
//
// Bare names (no slash) yield a single-element slice — that's how Docker
// Hub short-form works (`postgres:16` resolves to
// `docker.io/library/postgres:16`, and the customer-typed string is
// just `postgres:16`).
//
// The strip happens in two passes on purpose: we need to detect the
// registry (which may itself contain a `:` for the port) BEFORE we strip
// the image tag, otherwise the port colon would be mistaken for the
// tag separator.
func pathSegmentsAfterRegistry(ref string) []string {
	slash := strings.Index(ref, "/")
	if slash < 0 {
		// Bare name — strip tag/digest and return.
		return []string{stripTagDigest(ref)}
	}
	first := ref[:slash]
	if isRegistryHostname(first) {
		rest := ref[slash+1:]
		if rest == "" {
			return nil
		}
		return splitPathStrippingTrailingTag(rest)
	}
	// No registry prefix — the whole ref is path.
	return splitPathStrippingTrailingTag(ref)
}

// stripTagDigest returns ref with the trailing `:<tag>` or `@sha256:…`
// removed. Idempotent on already-stripped inputs.
func stripTagDigest(ref string) string {
	if i := strings.IndexAny(ref, "@:"); i >= 0 {
		return ref[:i]
	}
	return ref
}

// splitPathStrippingTrailingTag splits ref on `/` after stripping the
// trailing tag/digest from the LAST segment only. This way
// `myrepo/postgres:dev` becomes `["myrepo", "postgres"]` — only the
// last segment carries a tag.
func splitPathStrippingTrailingTag(ref string) []string {
	parts := strings.Split(ref, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		stripped := stripTagDigest(parts[i])
		if stripped != parts[i] {
			parts[i] = stripped
			break // only the trailing segment carries the tag
		}
	}
	return parts
}

// isRegistryHostname reports whether s looks like an OCI registry
// hostname. We use a deliberately tight predicate so we never strip
// a real path segment by accident:
//
//   - contains a `.`  → docker.io, ghcr.io, registry.example.com
//   - contains a `:`  → localhost:5000, registry:443
//   - equals "localhost"
//
// Bare names like `postgres` or `library` are NOT registries and are
// kept as path segments.
func isRegistryHostname(s string) bool {
	if s == "localhost" {
		return true
	}
	return strings.ContainsAny(s, ".:")
}

// errStatefulViolation is the typed sentinel the buildImageLayer handler
// returns to markDeployFailed. SentinelToCode lifts it to
// CodeStatelessOnlyViolation (422). Defined as a small var to keep
// fmt.Errorf-wrapping ergonomic at the call site.
var errStatefulViolation = oci.ErrStatelessOnlyViolation

// baseRefFor returns the canonical base image reference for a runtime. The
// empty runtime maps to the minimal base (plain apps, spec §4.6).
//
// go124-alpine is opt-in: customers who need the musl base set
// runtime=go124-alpine explicitly. The default go124 base stays
// bookworm (glibc) so existing deploys see no behavior change. A
// future PR may flip the default after measuring fleet-wide
// snapshot_fleet_avg_mb with both bases co-resident
// (pkg/api/limits.go::FleetSnapshotAvgTargetMB = 130, alarm 160).
func baseRefFor(runtime string) string {
	switch runtime {
	case RuntimeNode22:
		return BaseRefNode22
	case RuntimePython312:
		return BaseRefPython312
	case RuntimeGo124:
		return BaseRefGo124
	case RuntimeGo124Alpine:
		return BaseRefGo124Alpine
	case RuntimeNode24:
		return BaseRefNode24
	case RuntimePython313:
		return BaseRefPython313
	case RuntimeDebianParent:
		// ADR-053: parent runtime resolves to its own ref, never
		// BaseRefMinimal (which is `FROM scratch + COPY`, not on
		// the debian chain).
		return BaseRefDebianParent
	default:
		return BaseRefMinimal
	}
}

// resolveDeployBaseRef returns the OCI base ref to use when staging an
// image-deploy for the given runtime. Per-runtime env vars
// (FAAS_DEPLOY_BASE_REF_<RUNTIME>) take precedence over the const
// default in baseRefFor. Falls through to baseRefFor for runtimes
// not in DefaultRuntimeBaseRefs (the "" / BaseRefMinimal case for
// customer-uploaded images).
//
// Companion to baseRefFor (which is the pure const switch, pinned by
// TestBaseRefFor_Runtimes): wraps the const lookup with the same
// per-runtime env-override + digest-pin posture that EnsureBases
// (startup auto-stage, pkg/imaged/base_stage.go) uses. This keeps
// the Runtime → Ref → EnvOverride mapping in one place
// (DefaultRuntimeBaseRefs) and covers both the startup-stage and
// deploy-time paths with a single env var each, set via the
// cd-controlplane.yml writer loop.
//
// envLookup nil-falls-back to os.Getenv; the test seam is a map
// literal (TestResolveDeployBaseRef_*).
func resolveDeployBaseRef(runtime string, envLookup func(string) string) (string, error) {
	if envLookup == nil {
		envLookup = os.Getenv
	}
	if legacy := strings.TrimSpace(envLookup("FAAS_DEPLOY_BASE_REF")); legacy != "" {
		return "", fmt.Errorf("imaged: FAAS_DEPLOY_BASE_REF is retired; set the runtime-specific FAAS_DEPLOY_BASE_REF_<RUNTIME> instead")
	}
	if testRef := strings.TrimSpace(envLookup(testDeployBaseRefEnv)); testRef != "" {
		if node := strings.TrimSpace(envLookup("FAAS_NODE_NAME")); node != "" {
			return "", fmt.Errorf("imaged: %s is test-only and cannot be set on named node %q", testDeployBaseRefEnv, node)
		}
		return testRef, nil
	}
	for _, row := range DefaultRuntimeBaseRefs {
		if row.Runtime != runtime {
			continue
		}
		v := strings.TrimSpace(envLookup(row.EnvOverride))
		if v == "" {
			if err := requireProductionBaseDigest(row.EnvOverride, row.Ref, envLookup); err != nil {
				return "", err
			}
			return row.Ref, nil
		}
		parsed, perr := oci.ParseReference(v)
		if perr != nil || parsed.Digest == "" {
			return "", fmt.Errorf("imaged: %s=%q must be a digest-pinned reference (e.g. registry.gregale.dev/img@sha256:...)", row.EnvOverride, v)
		}
		return v, nil
	}
	// Runtime not in the table (e.g. customer-uploaded image with
	// runtime="") — fall through to baseRefFor's default (BaseRefMinimal).
	minimalEnv := "FAAS_DEPLOY_BASE_REF_MINIMAL"
	if v := strings.TrimSpace(envLookup(minimalEnv)); v != "" {
		parsed, perr := oci.ParseReference(v)
		if perr != nil || parsed.Digest == "" {
			return "", fmt.Errorf("imaged: %s=%q must be a digest-pinned reference (e.g. registry.gregale.dev/img@sha256:...)", minimalEnv, v)
		}
		return v, nil
	}
	ref := baseRefFor(runtime)
	if err := requireProductionBaseDigest(minimalEnv, ref, envLookup); err != nil {
		return "", err
	}
	return ref, nil
}

// ResolveDeployBaseRef exposes the canonical runtime-to-base resolver to the
// source builder. Keeping builderd and imaged on this one mapping is
// important: the OCI image produced by Railpack must begin with the same
// immutable base whose layers imaged removes before adding the app layer.
// The envLookup seam is retained for deterministic unit tests.
func ResolveDeployBaseRef(runtime string, envLookup func(string) string) (string, error) {
	return resolveDeployBaseRef(runtime, envLookup)
}

func requireProductionBaseDigest(envKey, ref string, envLookup func(string) string) error {
	if strings.TrimSpace(envLookup(testDeployBaseRefEnv)) != "" {
		if node := strings.TrimSpace(envLookup("FAAS_NODE_NAME")); node != "" {
			return fmt.Errorf("imaged: %s is test-only and cannot be set on named node %q", testDeployBaseRefEnv, node)
		}
		return nil
	}
	if strings.TrimSpace(envLookup("FAAS_NODE_NAME")) == "" {
		return nil
	}
	parsed, err := oci.ParseReference(ref)
	if err != nil || parsed.Digest == "" {
		return fmt.Errorf("imaged: %s must be a digest-pinned reference on named node %q (got %q)", envKey, envLookup("FAAS_NODE_NAME"), ref)
	}
	return nil
}
