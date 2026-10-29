/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OrgMemberResponse } from './OrgMemberResponse.js';
/**
 * GET /v1/orgs/{slug}/members response. Only active members (removed rows filtered).
 */
export type MemberListResponse = {
  members: Array<OrgMemberResponse>;
};

