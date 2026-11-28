/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerSignupPlan } from './ConsumerSignupPlan.js';
import type { MCPContractChangelogEntry } from './MCPContractChangelogEntry.js';
import type { MCPListingInstall } from './MCPListingInstall.js';
import type { MCPListingTool } from './MCPListingTool.js';
/**
 * A sold MCP server's public listing.
 */
export type PublicMCPListingResponse = {
  app: string;
  title: string;
  /**
   * What the listed server does.
   */
  summary: string;
  endpoint: string;
  tools: Array<MCPListingTool>;
  plans: Array<ConsumerSignupPlan>;
  trial_days?: number;
  livemode: boolean;
  signup_open: boolean;
  /**
   * The sign-up page, while sign-up is open.
   */
  signup_url?: string;
  install: MCPListingInstall;
  published_at: string;
  /**
   * Open customers, when the seller shows usage.
   */
  customers?: number;
  /**
   * Tool contract changes shipped since the listing was published, newest first.
   */
  changelog?: Array<MCPContractChangelogEntry>;
};

