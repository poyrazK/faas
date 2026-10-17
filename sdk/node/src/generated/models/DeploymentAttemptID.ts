/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Persist a random opaque ID before submission to recover lost acceptance
 * replies. The accepted attempt remains bound to its original deployment,
 * including after failure or supersession. Missing receipts never authorize
 * resubmission. Independent from Idempotency-Key replay.
 * Concurrent accepted/in-progress attempts may return 409 with
 * deployment_attempt_accepted or deployment_attempt_pending; recover
 * acceptance using the receipt instead of treating that conflict as rejection.
 *
 */
export type DeploymentAttemptID = string;
