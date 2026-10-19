/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Approved outbound CIDR ranges. Modes and override combinations are validated on publication.
 */
export type ApplicationStandardCIDRRule = {
  mode: 'default' | 'mandatory' | 'restricted';
  override?: 'none' | 'narrow';
  value: Array<string>;
};

