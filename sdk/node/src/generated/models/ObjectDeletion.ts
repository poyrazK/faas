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
  selector: '' | 'null';
  state: 'prepared' | 'dispatched' | 'completed' | 'failed';
  /**
   * Public marker UUID or null when acknowledged; private provider IDs are never exposed.
   */
  version_id?: string;
  delete_marker: boolean;
  last_error_code?: 'provider_uncertain' | 'configuration' | 'preparation_failed' | 'preparation_expired' | 'provider_rejected';
  created_at: string;
  updated_at: string;
};

