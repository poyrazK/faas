/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Approved extra outbound TCP ports. Modes and override combinations are validated on publication.
 */
export type ApplicationStandardExtraPortRule = {
  mode: 'default' | 'mandatory' | 'restricted';
  override?: 'none' | 'narrow';
  value: Array<number>;
};

