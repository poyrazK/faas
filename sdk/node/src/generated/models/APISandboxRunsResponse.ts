/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxReceiverInvite } from './APISandboxReceiverInvite.js';
import type { APISandboxRunReport } from './APISandboxRunReport.js';
/**
 * Recent scenario run reports for one release.
 */
export type APISandboxRunsResponse = {
  runs: Array<APISandboxRunReport>;
  receiver_invites?: Array<APISandboxReceiverInvite>;
};

