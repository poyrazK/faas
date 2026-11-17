/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-readable immutable credential binding; contains no plaintext token. Required scopes follow the existing 4-KiB review metadata bound.
 */
export type ToolGrantCredential = {
  sha256: string;
  /**
   * Static source ciphertext digest or managed connection authorization digest; source replacement or resealing invalidates the binding
   */
  source_sha256: string;
  /**
   * Present only for a managed execution OAuth connection
   */
  connection_id?: string;
  scope: 'default';
  required_scopes: Array<string>;
};

