/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit creation or UUID recovery through the app owner's connected Linear team.
 */
export type CreateNativeLinearIssueTicketRequest = {
  /**
   * Stable UUID v4 used as the Linear issue ID and reused across retries.
   */
  request_id: string;
  /**
   * Verified team UUID matching the stored app connection.
   */
  team_id: string;
  /**
   * Explicit ticket title bounded to 256 UTF-8 bytes.
   */
  title: string;
  /**
   * Optional description bounded to 16 KiB; no telemetry is copied automatically.
   */
  description?: string;
  /**
   * Explicitly recover an unconfirmed dispatch by its stable request UUID after the dispatch lease expires.
   */
  recover?: boolean;
  /**
   * Optional known Linear issue URL that also requests recovery and must match the UUID lookup.
   */
  recovery_url?: string;
};

