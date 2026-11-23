/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable deletion receipt; uncertain attempts retain their bucket fence.
 */
export type ObjectDeletion = {
  id: string;
  bucket_id: string;
  key: string;
  /**
   * Empty for ordinary deletion; null or the selected owned public version UUID otherwise.
   */
  selector: string;
  state: 'prepared' | 'dispatched' | 'completed' | 'failed';
  /**
   * Selected public version UUID or new public marker UUID or null when acknowledged; private provider IDs are never exposed.
   */
  version_id?: string;
  delete_marker: boolean;
  /**
   * object_protected defers a lifecycle target under retention or a hold until a later scan.
   */
  last_error_code?: 'provider_uncertain' | 'configuration' | 'preparation_failed' | 'preparation_expired' | 'provider_rejected' | 'object_protected';
  created_at: string;
  updated_at: string;
};

