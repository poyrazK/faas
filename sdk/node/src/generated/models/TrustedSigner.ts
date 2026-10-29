/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One entry in the per-app cosign trusted-publisher list. `name` is the
 * API-side label (matches `app_trusted_signers.signer_name`); `public_key_pem`
 * is the base64-encoded DER SubjectPublicKeyInfo bytes — NOT a PEM-armoured
 * block. The wire form strips the PEM armour at upload time so the
 * on-disk mirror (the image service reads `/etc/faas/secrets/trusted-publishers/`) is
 * a single blob per publisher. ECDSA P-256 only; the API rejects
 * other curves at PUT time with `trusted_signer_invalid`.
 *
 */
export type TrustedSigner = {
  /**
   * Lower-case label. Matches `app_trusted_signers.signer_name`. The label is the on-disk filename (without .pem) under /etc/faas/secrets/trusted-publishers/.
   */
  name: string;
  /**
   * Base64-encoded DER SubjectPublicKeyInfo. Bytes length must be in [64, 1024] (DB CHECK).
   */
  public_key_pem: string;
  /**
   * RFC3339 timestamp of when the operator onboarded this publisher.
   */
  added_at: string;
  /**
   * Account ID of the admin who onboarded this publisher (omit when not yet in audit log).
   */
  added_by?: string;
};

