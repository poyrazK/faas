/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Worker Credential.
 */
export type RealtimeWorkerCredential = {
  id: string;
  group_id: string;
  name: string;
  permissions: Array<'status' | 'claim' | 'renew' | 'complete' | 'retry' | 'dead_letters' | 'replay' | 'resync' | 'delete' | 'pause' | 'resume' | 'drain' | 'heartbeat' | 'release'>;
  generation: string;
  issued_at: string;
  expires_at: string;
  revoked_at: string | null;
  last_used_at: string | null;
  last_operation: string | null;
  use_count: string;
};

