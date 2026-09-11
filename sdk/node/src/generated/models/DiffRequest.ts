/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppManifest } from './AppManifest.js';
import type { BuildPlan } from './BuildPlan.js';
import type { CreateCronRequest } from './CreateCronRequest.js';
import type { CreateEdgeRuleRequest } from './CreateEdgeRuleRequest.js';
import type { DiffAppConfigPatch } from './DiffAppConfigPatch.js';
import type { DiffEnvRow } from './DiffEnvRow.js';
/**
 * JSON body for POST /v1/apps/{slug}/diff (PR-1). Slim
 * purpose-built DTO — every field maps 1:1 to a
 * [deploydiff.Pending] entry via the apid handler's adapter.
 * Empty / absent fields mean "no change proposed" (matches
 * the engine's pointer semantics: every nested field is
 * optional; null = "don't touch").
 *
 */
export type DiffRequest = {
  app_config?: DiffAppConfigPatch;
  /**
   * Would-write image reference. Empty = no image deploy
   * proposed. Compared against the baseline's
   * DeploymentResponse.ImageDigest for the immutable-diff
   * check.
   *
   */
  image?: string;
  manifest?: AppManifest;
  /**
   * Per-scope env write. Full-replacement semantics per
   * scope (ADR-090 D3). Keys are scope names ("default",
   * "staging", ...); values are DiffEnvRow arrays.
   *
   */
  env_by_scope?: Record<string, Array<DiffEnvRow>>;
  crons?: Array<CreateCronRequest>;
  edge_rules?: Array<CreateEdgeRuleRequest>;
  /**
   * Pending per-deployment env scope (ADR-091 / SAFE-RELEASES production-leveling Stream E). Compared against Baseline.LatestScope; mismatch emits a scope_mismatch break. Empty = default.
   */
  scope?: string | null;
  /**
   * Resolved source/workload intent for a deploy preview. Supplying this makes fresh app creation explicit even when resource values use server defaults.
   */
  build_plan?: BuildPlan;
};

