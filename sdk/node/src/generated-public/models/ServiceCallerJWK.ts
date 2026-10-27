/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public Ed25519 key used to verify a signed service-caller assertion.
 */
export type ServiceCallerJWK = {
  kty: 'OKP';
  crv: 'Ed25519';
  kid: string;
  'x': string;
  alg: 'EdDSA';
  use: 'sig';
};

