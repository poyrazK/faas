/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Canonical signed directory envelope. Existing quota envelopes are bounded to 8192 bytes; payloads to 4096 bytes. This transport never authorizes native execution.
 */
export type RegionalRuntimeQuotaEnvelope = {
  key_id: string;
  payload: string;
  signature: string;
};

