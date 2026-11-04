/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable public business correlation metadata. Captured at admission and preserved through recovery and redeploy. Never an ownership or authorization claim.
 */
export type OperationSubject = {
  type: string;
  /**
   * Opaque application identifier, compared exactly. Maximum 256 UTF-8 bytes; ASCII controls are rejected. Use a public stable ID appropriate for customer-visible history.
   */
  id: string;
};

