/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Worker Credential Request.
 */
export type RealtimeWorkerCredentialRequest = {
  name: string;
  permissions: Array<'status' | 'claim' | 'renew' | 'complete' | 'retry' | 'dead_letters' | 'replay' | 'resync' | 'delete' | 'pause' | 'resume' | 'drain' | 'heartbeat' | 'release'>;
  ttl_seconds?: number;
};

