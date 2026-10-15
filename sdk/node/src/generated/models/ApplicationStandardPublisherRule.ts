/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Permitted organization-owned signing keys. Modes and override combinations are validated on publication.
 */
export type ApplicationStandardPublisherRule = {
  mode: 'default' | 'mandatory' | 'restricted';
  override?: 'none' | 'narrow';
  value: Array<string>;
};

