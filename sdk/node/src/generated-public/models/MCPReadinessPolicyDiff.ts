/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessPolicyChange } from './MCPReadinessPolicyChange.js';
/**
 * Configuration differences between the selected readiness policy revisions.
 */
export type MCPReadinessPolicyDiff = {
  scope: string;
  from_revision: string;
  to_revision: string;
  changes: Array<MCPReadinessPolicyChange>;
};

