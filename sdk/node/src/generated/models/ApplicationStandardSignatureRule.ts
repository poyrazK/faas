/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Image signature requirement. Modes and override combinations are validated on publication.
 */
export type ApplicationStandardSignatureRule = {
  mode: 'default' | 'mandatory' | 'restricted';
  override?: 'none' | 'narrow';
  value: boolean;
};

