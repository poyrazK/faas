/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-038 / Tier 3 / issue #197 B3.10-read half: the
 * post-mortem "what ran?" record for a single successful build.
 * Field names mirror the `build_provenance` table columns. Empty
 * strings indicate a column the populator hasn't filled yet —
 * `buildkit_version`, `railpack_version`, `base_digest`,
 * `runner_digest`, and `sbom_storage_key` are populated by
 * Phase 3 (cosign signer + syft SBOM), but the columns exist
 * today so Phase 3 is a zero-cost schema change.
 * `framework_version` is the language version the customer's
 * source declares (`.nvmrc`, `package.json::engines.node`,
 * `pyproject.toml::requires-python`, `.python-version`,
 * `.tool-versions`, `go.mod` directive); added in issue #740 /
 * DEPLOY-PROV-5 / ADR-087. OBSERVATIONAL ONLY — the build
 * pipeline never reads it (the runtime is bound by the OCI
 * base ref via `FAAS_DEPLOY_BASE_REF_<RUNTIME>`).
 *
 */
export type BuildProvenanceResponse = {
  id: string;
  build_id: string;
  buildkit_version?: string;
  railpack_version?: string;
  base_digest?: string;
  /**
   * sha256 of the customer's source tarball (the cache lookup key).
   */
  source_sha256: string;
  source_url?: string;
  commit_sha?: string;
  /**
   * free / hobby / pro / scale — copied from the account at claim time.
   */
  plan: string;
  runner_digest?: string;
  /**
   * compute_node name (default `default-local` on the one-box).
   */
  builder_node_id: string;
  started_at: string;
  finished_at: string;
  /**
   * Phase 3 populator fills this from `syft` output. Empty string when not yet populated.
   */
  sbom_storage_key?: string | null;
  /**
   * Source-declared language version (nodes 22.11.0 / python 3.13 / go 1.24, etc.). Empty string when no version file is present or any parser fails — best-effort, never an error. Added in DEPLOY-PROV-5 / issue #740 / ADR-087.
   */
  framework_version?: string | null;
};

