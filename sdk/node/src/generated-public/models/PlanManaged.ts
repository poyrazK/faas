/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A service the repo declared (compose image:, render.yaml, etc.)
 * but the platform will NOT provision. Surfaced so the customer
 * sees them in the plan and reads a learnable env hint instead of
 * a runtime 422 (two-drive FROM-base contract, ADR-046).
 *
 */
export type PlanManaged = {
  name: string;
  kind: string;
  env_hint: string;
  source: string;
  image: string;
};

