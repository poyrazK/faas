/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Rotate a secret: new plaintext value (ADR-089 PR-B). Same wire
 * shape as `PutAppSecretRequest`; the rotate verb is distinct so
 * the server can emit the `secret.rotated` audit kind (vs
 * `secret.set` on PUT). Byte cap is the per-plan
 * `SecretValueMaxBytes`.
 *
 */
export type RotateAppSecretRequest = {
  /**
   * New plaintext. Sealed server-side; never persisted in plaintext.
   */
  value: string;
};

