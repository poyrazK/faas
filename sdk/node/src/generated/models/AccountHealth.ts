/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountHealthDeployment } from './AccountHealthDeployment.js';
import type { AccountHealthDomain } from './AccountHealthDomain.js';
import type { AccountHealthRequests } from './AccountHealthRequests.js';
import type { AccountHealthRestriction } from './AccountHealthRestriction.js';
import type { AccountHealthSignal } from './AccountHealthSignal.js';
/**
 * What may stop the calling account from working: restrictions with the next step, problems that keep recurring, failed deployments, custom-domain problems and refused API requests.
 */
export type AccountHealth = {
  /**
   * When the checks ran.
   */
  generated_at: string;
  /**
   * Number of restrictions, signals, failed deployments and domain issues; zero means nothing needs action.
   */
  attention: number;
  /**
   * Restrictions currently applied to the account, one per kind.
   */
  restrictions: Array<AccountHealthRestriction>;
  /**
   * Problems that keep recurring on the account, such as repeated failed deployments or out-of-memory stops, with the next step. Absent from older servers.
   */
  signals?: Array<AccountHealthSignal>;
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

