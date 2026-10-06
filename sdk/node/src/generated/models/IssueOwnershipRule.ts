/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provide at least one matcher. All populated matchers are ANDed; the first matching rule assigns a new issue to one eligible account.
 */
export type IssueOwnershipRule = {
  /**
   * Exact exception type match.
   */
  exception_type?: string;
  source_kind?: 'exception' | 'http' | 'runtime' | 'worker';
  /**
   * Path prefix; matches only at a path-segment boundary.
   */
  route_prefix?: string;
  assignee_account_id: string;
};

