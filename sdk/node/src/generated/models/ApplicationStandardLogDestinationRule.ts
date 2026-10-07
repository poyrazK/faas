/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Organization-owned logging destination references. Modes and override combinations are validated on publication.
 */
export type ApplicationStandardLogDestinationRule = {
  mode: 'default' | 'mandatory';
  override?: 'none' | 'extend';
  value: Array<string>;
};

