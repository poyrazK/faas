/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Set a secret: key name and plaintext (sealed at rest immediately, plaintext discarded after seal).
 */
export type PutAppSecretRequest = {
  /**
   * Plaintext. Sealed server-side; never persisted in plaintext.
   */
  value: string;
};

