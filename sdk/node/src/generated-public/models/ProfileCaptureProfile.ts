/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One collector's stored profile.
 */
export type ProfileCaptureProfile = {
  kind: 'cpu' | 'heap' | 'stacks' | 'block' | 'mutex';
  process_id?: string;
  bytes: number;
};

