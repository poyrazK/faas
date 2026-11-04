/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Set a secret: key name and plaintext (sealed at rest immediately, plaintext discarded after seal). secret_class is optional; omission preserves an existing class and defaults new rows to persistent. Ephemeral values prevent future init/warm captures for the app scope; older artifacts age out under normal snapshot garbage collection.
 */
export type PutAppSecretRequest = {
  /**
   * Plaintext. Sealed server-side; never persisted in plaintext.
   */
  value: string;
  /**
   * Optional snapshot-retention policy. Ephemeral secrets cause their runtime VM to be destroyed instead of creating warm or init snapshots when parked; new requests cold-boot.
   */
  secret_class?: 'persistent' | 'ephemeral';
};

