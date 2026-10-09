/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileInvestigationInput } from './ProfileInvestigationInput.js';
/**
 * Complete replacement. Supply expected_revision 0 for creation or the current revision for updates. Concurrent changes return conflict. Body limited to 65536 bytes.
 */
export type SaveProfileInvestigationRequest = {
  expected_revision: number;
  investigation: ProfileInvestigationInput;
};

