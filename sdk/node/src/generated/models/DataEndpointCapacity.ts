/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Credential-free shared physical allocation and initial-copy peak demand. Other Gregale reservations remain counted while paused or awaiting cleanup.
 */
export type DataEndpointCapacity = {
  state: 'ready' | 'insufficient_capacity' | 'needs_qualification';
  usable_connections: number;
  reserved_connections: number;
  required_connections: number;
  limiting_resource?: 'connections' | 'replication_slots' | 'wal_senders' | 'logical_workers';
  observed_at?: string;
  expires_at?: string;
};

