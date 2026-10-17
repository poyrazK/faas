# ADR-1113 · Deployment-local issue source maps

- **Status:** accepted
- **Date:** 2026-10-11
- **Decision:** Let the Node issue reporter register bounded, flat Source Map v3
  artifacts from its own deployment when using failure-site grouping. Resolve
  generated line/column locations to original file, function, and line before
  reporting structured frames. Keep the raw stack trace as generated evidence.
- **Why:** Bundled filenames and minified symbols vary across builds, splitting
  one failure into multiple groups. Deployment-local maps share the lifetime of
  the running code without requiring a new cross-release artifact registry.
- **Consequences:** Map registration is immutable per reporter and matches exact
  absolute generated filenames or file URLs; basename matching is forbidden.
  Relative original sources are resolved against the generated file and
  sourceRoot. A missing original name becomes anonymous rather than preserving
  a changing minified symbol. Dependency sources remain excluded from app
  grouping. Missing entries and unmapped segments retain generated frames.
  Limits live in pkg/api/limits.go and are mirrored by the Node helper. A pinned
  trace-mapping parser adds one runtime dependency to the Node SDK; registration
  validates VLQs, coordinate/index bounds, and decoded resource limits. Maps are
  parsed locally, source content is discarded, and no map URL is fetched or
  embedded source content uploaded. Original frames pass through existing server validation
  and redaction. The dashboard presents reported frame locations alongside raw
  stack evidence. Existing grouping versions and saved history are unchanged;
  enabling mapping can create new groups because evidence changes. Roll out the
  same map configuration with each deployment's reporting token.
  Server artifact upload, indexed maps, OTLP symbolication,
  and history-preserving migration remain follow-up work.
- **Rejected alternatives:** Fetching sourceMappingURL during capture adds
  network work and permits unrelated map content to determine identity. Matching
  by basename can select another bundle's map. Keeping minified function names
  when a map omits names preserves false splits across builds. Applying maps to
  legacy reporters silently changes default grouping evidence. Node's nearest
  entry API alone can carry a prior mapping into unmapped regions or lines.

## 2026-10-13 extension: automatic discovery and deployment verification

The Node SDK discovers local JS bundle annotations and adjacent hidden maps
during a build, then emits a portable versioned manifest containing only mapping
metadata and exact bundle SHA-256 digests. Source content and producer extensions
are excluded. The startup loader verifies the deployed bundle bytes, relocates
relative bundle paths, rebases map-relative sources, and registers maps before
capture. Both failure-site and cause-chain grouping support this configuration;
manual maps and manifest loading are mutually exclusive. Legacy grouping remains
unchanged. This is deployment-local symbolication; it does not add centralized
artifact storage or server-side OTLP processing.

Discovery is bounded to 4,096 tree entries, 16 MiB per bundle, and 64 MiB of
bundle bytes, in addition to existing map limits. Manifest JSON is bounded to
4 MiB plus 64 KiB. Symlinks and dependency directories are excluded; referenced
maps cannot escape the build tree. Remote and inline maps are rejected without
fetching, and no customer code executes. Portable maps use relative or virtual
original paths rather than absolute build-machine paths. Build/runtime trees
must be immutable during reading. Digest verification detects stale or different
bundle bytes, not a malicious manifest producer. The build CLI requires explicit
`--replace` to replace an existing output, and failed discovery keeps it intact.
