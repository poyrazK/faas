/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppManifest } from './AppManifest.js';
/**
 * An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy pointer, per-app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169 / #172).
 */
export type AppResponse = {
  id: string;
  slug: string;
  type: 'app' | 'function';
  /**
   * Runtime for `type: function` apps. Omit for `type: app` (the default).
   */
  runtime?: 'node22' | 'python312' | 'go124';
  ram_mb: number;
  max_concurrency: number;
  idle_timeout_s?: number | null;
  min_instances: number;
  status: string;
  url: string;
  manifest: AppManifest;
  /**
   * Per-app outbound CIDR allowlist (ADR-031 + ADR-032). Each entry is a CIDR string — v4 (`1.2.3.0/24`) or v6 (`2001:db8::/32`). v4-mapped v6 form (`::ffff:1.2.3.0/120`) is silently canonicalised to its v4 form at write time. Empty array means no allowlist rule; the per-netns chain's default-accept policy applies.
   */
  egress_allowlist?: Array<string>;
  /**
   * Per-instance RPS target for the reactive scale-up trigger. 0 = disabled. Hobby/Pro/Scale only. When measured per-instance RPS exceeds this value, schedd admits another instance (up to max_concurrency). See ADR-037.
   */
  autoscale_target_rps: number;
  /**
   * Per-instance CPU% target (1..100) for the reactive scale-up trigger. 0 = disabled. Pro/Scale only. When measured per-instance CPU% exceeds this value, schedd admits another instance (up to max_concurrency). See ADR-037.
   */
  autoscale_target_cpu_pct: number;
};

