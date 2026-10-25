/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OrgInvitationResponse } from './OrgInvitationResponse.js';
/**
 * GET /v1/orgs/{slug}/invitations response. Sorted by created_at DESC.
 */
export type InvitationListResponse = {
  invitations: Array<OrgInvitationResponse>;
  /**
   * Opaque cursor — set to the **base64-url-of-JSON** of
   * the last row's compound `(created_at, id)` key when
   * there's a next page. Pass back as `?before=` to
   * fetch it. PR-9 changes the encoding from the v1
   * bare-id cursor to the compound key so the
   * `created_at DESC, id DESC` order is preserved
   * exactly under random UUIDs. The wire shape is
   * opaque; clients MUST pass the value back unchanged.
   *
   */
  next_before?: string;
};

