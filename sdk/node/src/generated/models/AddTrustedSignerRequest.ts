/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PUT body for `/v1/apps/{slug}/trusted_signers/{name}`. `public_key_pem` is
 * the base64-encoded DER blob (the API side strips PEM armour). The DER must
 * parse as an ECDSA P-256 SPKI; any other curve or non-ECDSA key returns
 * 400 `trusted_signer_invalid`. Bytes length must land in [64, 1024].
 *
 */
export type AddTrustedSignerRequest = {
  /**
   * Base64-encoded DER SubjectPublicKeyInfo. Length bounds match the DB CHECK.
   */
  public_key_pem: string;
};

