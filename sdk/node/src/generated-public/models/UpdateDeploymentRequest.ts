/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for PATCH /v1/deployments/{id} (issue #557 closure / ADR-072). The only mutable field post-create is the per-deployment cold-wake floor; image / digest / overrides / sidecars stay immutable.
 */
export type UpdateDeploymentRequest = {
  /**
   * Per-deployment cold-wake floor override for PATCH /v1/deployments/{id}. 0 = inherit from parent app; positive value is the deployment's own floor. Effective per-instance floor = max(app, deployment). Validated against the parent app's plan MaxMinInstances cap.
   */
  min_instances: number;
};

