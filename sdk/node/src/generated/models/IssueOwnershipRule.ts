/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provide at least one matcher. All populated matchers are ANDed; the first matching rule assigns a new issue to one eligible account or team. Exactly one assignment target is required.
 */
export type IssueOwnershipRule = {
  assignee_account_id?: string;
  /**
   * Exact exception type match.
   */
  exception_type?: string;
  source_kind?: 'exception' | 'http' | 'runtime' | 'worker';
  /**
   * Path prefix; matches only at a path-segment boundary.
   */
  route_prefix?: string;
  /**
   * Local source path prefix (256 UTF-8 bytes); matches any application frame in the exception or its causes at a path-segment boundary. Backslashes and dot segments normalize; parent traversal, URLs, queries, and fragments are rejected. No glob or filesystem access.
   */
  source_path_prefix?: string;
  /**
   * Exact allowlisted operational key=value matcher against sanitized reported tags; value <=128 UTF-8 bytes. Missing keys do not match an empty value.
   */
  tag?: string;
  /**
   * Team selected by this ownership rule; cannot be combined with an account assignment.
   */
  assignee_team_id?: string;
};

