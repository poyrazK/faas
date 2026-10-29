/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in keyset_v1 metadata. App scoping and canonical boot selection precede the exclusive timestamp/event-ID seek and page limit. Complete means no further retained row in this live read, not complete capture or a frozen snapshot. Native event IDs stay inside the opaque cursor.
 */
export type WakeTimelinePage = {
  mode: 'keyset_v1';
  /**
   * Normalized original exclusive lower bound; omitted when no lower bound was supplied.
   */
  since?: string;
  /**
   * Echo of the supplied opaque page cursor.
   */
  cursor?: string;
  /**
   * Opaque continuation for the same account/app/wake/since/limit. Omitted on terminal retained pages.
   */
  next_cursor?: string;
  complete: boolean;
};

