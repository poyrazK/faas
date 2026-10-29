/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One-shot enrollment payload. Returned exactly once on
 * /enroll. The server persists `mfa_secret_encrypted` (sealed)
 * and `mfa_recovery_codes_hash` (SHA-256d); subsequent calls
 * to /enroll overwrite the secret + codes but do NOT
 * re-surface the plaintexts to the dashboard.
 *
 */
export type MFAEnrollResponse = {
  /**
   * Standard `otpauth://totp/...` URL with the issuer = FaaS.
   * The customer's authenticator app ingests this on its own;
   * the dashboard also embeds the QR for camera-based setup.
   *
   */
  otpauth_url: string;
  /**
   * Base32-encoded TOTP secret, 32 chars (no padding). Same
   * value embedded in the otpauth URL; surfaced here so the
   * dashboard can render the secret directly for
   * copy-paste into an authenticator app that doesn't read
   * URLs.
   *
   */
  secret: string;
  /**
   * Base64-encoded PNG bytes of the QR code (256×256). The
   * server base64-encodes the raw PNG for JSON transport;
   * the dashboard decodes the string back to bytes before
   * rendering it in an `<img>` tag. The authenticator scans
   * the decoded PNG.
   *
   */
  qr_code_png_base64: string;
  /**
   * Ten single-use 10-character base32 strings. The
   * dashboard renders them in the "save these somewhere"
   * step. Each code is hashed (SHA-256) before storage;
   * the plaintext never reappears.
   *
   */
  recovery_codes: Array<string>;
};

