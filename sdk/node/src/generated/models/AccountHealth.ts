/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountHealthDeployment } from './AccountHealthDeployment.js';
import type { AccountHealthDomain } from './AccountHealthDomain.js';
import type { AccountHealthRequests } from './AccountHealthRequests.js';
import type { AccountHealthRestriction } from './AccountHealthRestriction.js';
/**
 * What may stop the calling account from working: restrictions with the next step, failed deployments, custom-domain problems and refused API requests.
 */
export type AccountHealth = {
  /**
   * When the checks ran.
   */
  generated_at: string;
  /**
   * Number of restrictions, failed deployments and domain issues; zero means nothing needs action.
   */
  attention: number;
  /**
   * Restrictions currently applied to the account, one per kind.
   */
  restrictions: Array<AccountHealthRestriction>;
  /**
   * Failed deployments from the last 7 days, newest first, at most 10.
   */
  failed_deployments: Array<AccountHealthDeployment>;
  /**
   * Custom domains that are unverified or whose certificate is not serving.
   */
  domain_issues: Array<AccountHealthDomain>;
  refused_requests: AccountHealthRequests;
  /**
   * Checks that could not be loaded; their lists are empty and mean unknown, not none.
   */
  unavailable?: Array<string>;
};

