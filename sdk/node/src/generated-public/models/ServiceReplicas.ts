/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-deployment replica scaffold for execution_mode='service'. Replica count is bounded by ServiceReplicasMax per plan (Hobby 3, Pro 5, Scale 20), and desired must also fit the app's max_concurrency ceiling. min ≤ desired ≤ max must hold. Foundation here; rolling-deploy / rollback / image-digest pinning semantics land.
 */
export type ServiceReplicas = {
  /**
   * Minimum desired replicas the Engine keeps alive. 0 = no minimum.
   */
  min: number;
  /**
   * Maximum desired replicas (engine-side cap; service autoscale bounds).
   */
  max: number;
  /**
   * Desired replica count. Engine wakes replacement instances to maintain this when one fails or is destroyed.
   */
  desired: number;
};

