/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkloadDependency } from './WorkloadDependency.js';
/**
 * One entry in the deploy request's `sidecars` array
 * (issue #463 / ADR-068). Up to 2 sidecars per app (1 init
 * + 1 sidecar; the array is type-uniqueness + 2-capped at
 * the schema layer via migration 00095's CHECK constraint).
 * Stateless only — stateful base images (Postgres, Redis,
 * MySQL, MongoDB, etc.) are rejected at the API gate
 * with 403 `sidecar_stateful_denied` and again at imaged
 * (PR-B). Image references must be digest-pinned
 * (`repo@sha256:...`); tag references are rejected with
 * 400 `sidecar_invalid_image`. Env values are
 * envelope-sealed at rest via secretbox (namespace
 * `"sidecar_env"`); the wire shape is plaintext, the
 * column is sealed ciphertext.
 *
 * - `name` matches RFC 1123 label (lowercase alphanumeric
 * + dash, 1..63 chars, starts with [a-z0-9]). Unique
 * within a single request.
 * - `image` is the digest-pinned OCI reference. Tag
 * references rejected. State images rejected.
 * - `type` ∈ {`init`, `sidecar`}. At most one of each per
 * deployment.
 * - `cmd` is the argv (image's ENTRYPOINT unchanged; CMD
 * overridden). Every element non-empty.
 * - `env` is plaintext on the wire, sealed at rest. Keys
 * per `^[A-Z][A-Z0-9_]*$`; per-value byte cap = plan
 * `EnvValueMaxBytes`. Plaintext values NEVER appear in
 * any log, audit, or error.
 * - `port` ∈ {0, 1..65535}. 0 = absent.
 * - `ram_mb` ∈ {0, 32..512}. 0 = inherit plan RAM.
 * - `cpu_millicores` ∈ {0, 250, 500, 1000}. 0 = inherit app CPU quota.
 * - `essential` defaults to true. If true and the workload
 * exits non-zero, the dependency set fails
 * (`failure_class=user_error`) and essential long-running
 * sidecars restart-loop. If false, the failure is logged
 * and the other workloads continue.
 * - `depends_on` optionally gates this workload on `main` or
 * another sidecar. Conditions are `started`, `healthy`, and
 * `completed_successfully`; omitted condition means `started`.
 * Init workloads are implicit prerequisites of main and long-running
 * sidecars. Cycles and unknown workload names are rejected.
 *
 */
export type Sidecar = {
  /**
   * RFC 1123 label (lowercase alphanumeric + dash, 1..63 chars, starts with [a-z0-9]).
   */
  name: string;
  /**
   * Digest-pinned OCI reference (repo@sha256:...). Tag references rejected with 400 `sidecar_invalid_image`.
   */
  image: string;
  /**
   * `init` runs once before the main workload (DB migrator shape). `sidecar` runs alongside (metrics scraper shape).
   */
  type: 'init' | 'sidecar';
  /**
   * Argv. Image's ENTRYPOINT unchanged; CMD overridden. Every element non-empty.
   */
  cmd?: Array<string>;
  /**
   * Plaintext env map (sealed at rest). Keys `^[A-Z][A-Z0-9_]*$`; per-value byte cap = plan EnvValueMaxBytes.
   */
  env?: Record<string, string>;
  /**
   * Listen port. 0 = absent / fall back to image default.
   */
  port?: number;
  /**
   * Cgroup memory ceiling for this sidecar. 0 = inherit plan RAM; 32..512 enforced at the API.
   */
  ram_mb?: number;
  /**
   * Sustained cgroup CPU allowance in millicores. 0 = inherit app CPU quota.
   */
  cpu_millicores?: 0 | 250 | 500 | 1000;
  /**
   * Defaults to true. Essential workload failure fails the set; non-essential failure is logged and contained.
   */
  essential?: boolean;
  /**
   * Optional workload lifecycle dependencies. Init workloads are implicit prerequisites of main and long-running sidecars.
   */
  depends_on?: Array<WorkloadDependency>;
};

