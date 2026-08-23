/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Structured detection trace for one workload (issue #742).
 * `source` on PlanWorkload carries the same provenance as free
 * text ("compose.yaml: api"); this is the machine-readable form
 * so a client can branch on the detector without parsing it.
 *
 * Additive and optional: absent on any response the server did
 * not populate, so existing consumers are unaffected.
 *
 */
export type PlanDetectedBy = {
  /**
   * closed vocabulary — the detector that won identity for this workload
   */
  detector: 'compose' | 'procfile' | 'k8s' | 'render' | 'fly' | 'serverless' | 'app_yaml' | 'other';
  /**
   * The detector's tiebreak weight; higher wins identity
   * within a tier. Surfaced so the precedence order is visible
   * on the wire rather than implied by server source.
   *
   */
  priority: number;
  /**
   * Other detectors whose seeds collapsed into this workload
   * under the (root_dir, name) merge key, deduplicated and
   * deterministically ordered. Absent when the workload came
   * from a single seed. Answers "why didn't my Procfile `web`
   * get its own workload?" — it merged into the compose `web`.
   *
   */
  merged_from?: Array<'compose' | 'procfile' | 'k8s' | 'render' | 'fly' | 'serverless' | 'app_yaml' | 'other'>;
};

