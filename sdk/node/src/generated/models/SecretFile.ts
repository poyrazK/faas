/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An app secret guest-init writes to /run/secrets/<name> in the main workload, from a Compose service's secrets. Names the secret; its value is never returned.
 */
export type SecretFile = {
  /**
   * App secret whose value the file holds.
   */
  secret: string;
  /**
   * File name in /run/secrets.
   */
  name: string;
  /**
   * Permission bits; omitted means 0444.
   */
  mode?: number;
};

