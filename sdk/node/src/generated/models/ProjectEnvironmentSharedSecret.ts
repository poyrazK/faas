/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata of one secret shared by every service of a project environment. The value is write-only.
 */
export type ProjectEnvironmentSharedSecret = {
  key: string;
  /**
   * Increases on every value change.
   */
  version: number;
  /**
   * Recipient of the fleet key that sealed the value.
   */
  kid: string;
  /**
   * HMAC fingerprint of the value, for comparing values without revealing them.
   */
  value_hash: string;
  created_at: string;
  updated_at: string;
};

