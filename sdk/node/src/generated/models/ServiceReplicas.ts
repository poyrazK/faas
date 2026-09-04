/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-deployment replica scaffold for execution_mode='service' (ADR-137 §Decision 3, M-2 + M-4 workstream E). Replica count is bounded by ServiceReplicasMax per plan (Hobby 3, Pro 5, Scale 20). min ≤ desired ≤ max must hold. Foundation here; rolling-deploy / rollback / image-digest pinning semantics land in M-4.
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

