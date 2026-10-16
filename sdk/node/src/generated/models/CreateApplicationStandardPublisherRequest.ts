/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An approved ECDSA P-256 public signing key.
 */
export type CreateApplicationStandardPublisherRequest = {
  name: string;
  /**
   * Base64 ECDSA P-256 SubjectPublicKeyInfo DER; private keys and unsupported curves are rejected.
   */
  public_key_der: string;
};

