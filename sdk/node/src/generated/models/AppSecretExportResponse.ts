/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One sealed secret in export form: key name, sealed ciphertext (sealed at rest per §11), version, and timestamps. Plaintext is never exported.
 */
export type AppSecretExportResponse = {
  app_id: string;
  key: string;
  /**
   * base64-encoded age-sealed envelope
   */
  ciphertext: string;
  created_at: string;
  updated_at: string;
};

