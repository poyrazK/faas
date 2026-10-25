/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Set a private-registry Basic Auth credential: normalized registry
 * host + username + plaintext password. The password is sealed
 * server-side under namespace `"registry_creds"` against the host
 * X25519 recipient and never persisted in plaintext.
 *
 */
export type PutAppRegistryCredentialRequest = {
  /**
   * Registry host. Must include explicit `https://` prefix (schemeless + http:// are rejected per ADR-062 §https-only clarification; customer's Basic Auth never leaves the box over cleartext). Trailing slash optional; embedded path / query / fragment rejected.
   */
  registry: string;
  /**
   * Basic Auth username (metadata, NOT sealed).
   */
  username: string;
  /**
   * Plaintext Basic Auth password. Sealed server-side; never persisted or returned.
   */
  password: string;
};

