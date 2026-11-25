/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * What an account deployed without an owner may use until it is claimed.
 */
export type UnclaimedLimits = {
  plan: string;
  max_apps: number;
  max_ram_mb: number;
  blocked: Array<string>;
};

