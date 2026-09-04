/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppManifestHealthcheck } from './AppManifestHealthcheck.js';
import type { ServiceReplicas } from './ServiceReplicas.js';
/**
 * App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-as-source flag (§ux 6.3). The optional `env_secrets` field carries sealed-secret refs ("secret:NAME" strings) resolved by the host at wake time against the app_secrets table (issue #460 / ADR-053 §Decision 1). Values are NEVER sealed ciphertext — only refs. M-1 (ADR-136) widens the contract additively with `healthcheck`, `stop_signal`, `stop_grace_period` from the OCI image-config spec; old guest-init ignores unknown fields per JSON semantics, so the widen is wire-compatible. M-2 (ADR-137 + ADR-138) widens additively with `execution_mode`, `restart_policy`, `startup_deadline_s`, `max_retries`, and `service_replicas` — these govern the lifecycle contract (request vs service vs worker vs job) and the per-mode replica scaffold. Defaults preserve today's behaviour (execution_mode=request, restart_policy=on-failure).
 */
export type AppManifest = {
  entrypoint: Array<string>;
  env?: Record<string, string>;
  /**
   * Env override via sealed-secret refs. Each value is "secret:NAME"; the host resolver looks up NAME against the app_secrets table at wake.
   */
  env_secrets?: Record<string, string>;
  working_dir?: string | null;
  port?: number | null;
  healthz?: string | null;
  user?: string | null;
  healthcheck?: AppManifestHealthcheck;
  /**
   * OCI STOPSIGNAL (default SIGTERM). Wired into the Engine.StopInstance signal-and-grace flow in M-2.
   */
  stop_signal?: string | null;
  /**
   * OCI StopGracePeriod as a Go duration string (e.g. "30s"). Per-plan cap (Hobby 30s, Pro 60s, Scale 120s) enforced by Validate() — ADR-138 §Decision 4.
   */
  stop_grace_period?: string | null;
  /**
   * Lifecycle contract for this app (ADR-137 §Decision 1). Default 'request' preserves today's behaviour.
   */
  execution_mode?: 'request' | 'service' | 'worker' | 'job';
  /**
   * Restart behaviour when the main workload exits (ADR-137 §Decision 2). Default is mode-derived: always for worker/service, no for job, on-failure for request.
   */
  restart_policy?: 'no' | 'on-failure' | 'always' | 'unless-stopped';
  /**
   * Upper bound on time-to-ready (seconds). Per-plan cap enforced by Validate() (ADR-138 §Decision 3). Default 0 means 'use plan default'.
   */
  startup_deadline_s?: number | null;
  /**
   * Consecutive restart-attempt cap (ADR-138 §Decision 3). Per-plan cap: Hobby 5, Pro 10, Scale 20. Default 0 means 'use plan default'.
   */
  max_retries?: number | null;
  service_replicas?: ServiceReplicas;
};

