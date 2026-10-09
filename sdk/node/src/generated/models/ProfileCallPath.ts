/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCallPathFrame } from './ProfileCallPathFrame.js';
/**
 * Root-to-frame selection. Named comparison frames ignore sampled line changes; candidate and anonymous frames match their sampled line. Total name and file text is limited to 16384 UTF-8 bytes.
 */
export type ProfileCallPath = {
  view: 'candidate' | 'comparison';
  frames: Array<ProfileCallPathFrame>;
};

