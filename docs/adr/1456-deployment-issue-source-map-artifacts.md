# ADR-1456 · Deployment-bound Issues source-map artifacts

- **Status:** accepted
- **Date:** 2026-10-13
- **Decision:** Store a sanitized, immutable source-map manifest for each app and
  deployment. Owner-scoped REST/CLI/SDK publication requires deploy-write access
  and session MFA. REST and OTLP captures opt into `deployment-source-maps-v1`
  together with failure-site or cause-chain grouping. Ingest credentials choose
  the deployment; event bodies cannot choose another artifact set.
- **Why:** Deployment-local Node reporters can resolve bundles before capture,
  but OTLP exceptions need server-side mapping. Publishing before instrumentation
  starts prevents artifact arrival order from silently changing group identity.
- **Bounds:** Existing flat-map limits apply: 32 maps, 512 KiB per input map,
  4 MiB in aggregate, 100,000 decoded rows plus segments, and 8,192 sources plus
  names. The upload envelope is at most 4 MiB plus 64 KiB. At most 32 ready
  deployment sets exist per app. Limits live in `pkg/api/limits.go`. Retirement
  removes mapping blobs and frees ready-set capacity while retaining their
  immutable digest. Only the identical canonical manifest may restore them.
- **Persistence:** A PostgreSQL table binds artifacts to `(deployment_id,app_id)`
  with a composite deployment-owner foreign key. Source contents and producer
  extensions are omitted. The app lock serializes publication, retirement,
  quotas, and ingestion. Artifact rows are operational evidence and are not
  cloned; deployment cleanup accounts for their foreign-key dependency.
- **Evidence:** Columns are retained only when known and are excluded from
  identity. Node raw stacks provide columns for opted-in server mapping. Exact
  file matching and nearest same-line segments determine original file, symbol,
  line, and column; unmapped ranges remain generated. Both outer and cause
  frames are supported. All mapped evidence passes normal sanitization before
  fingerprinting; raw stack evidence and earlier redaction markers survive.
  Occurrences record the canonical manifest digest and mapping counts.
- **Retries and lifecycle:** Missing or retired sets reject new opted-in captures
  with `409 issue_source_maps_unavailable`. Retained exact retries resolve their
  original occurrence before loading artifacts. Original payload hashes remain
  based on submitted evidence. Different manifests conflict even after retirement.
  App or deployment deletion cascades artifact rows; occurrence evidence already
  contains its mapped frames. No historical regrouping or legacy-default change.
- **Trust:** The canonical digest identifies uploaded mapping metadata. Bundle
  digests are producer claims; the server neither retrieves bundle bytes nor
  attests that mappings match deployed code. Node manifest loading separately
  verifies local bundle bytes. No remote URL fetch or customer-code execution
  occurs. Indexed maps and automatic build/export handoff remain future work.
