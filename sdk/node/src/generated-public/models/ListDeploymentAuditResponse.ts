/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentAuditResponse } from './DeploymentAuditResponse.js';
/**
 * Paginated wrapper for `GET /v1/deployments/{id}/audit`
 * (issue #976 / ADR-122 / SAFE-RELEASES-E.2 + production-
 * leveling Stream A). Limit is echoed back so a paging
 * consumer can distinguish "limit was clamped" from "no
 * more rows" — both yield Items of length < limit, but the
 * clamping is observable via this field.
 *
 */
export type ListDeploymentAuditResponse = {
  items: Array<DeploymentAuditResponse>;
  /**
   * Echo of the server-applied limit (query param ?limit= clamps here).
   */
  limit: number;
};

