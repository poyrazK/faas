/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical SQL writer inventory and optional provider guard evidence. Confirmed requires fresh matching evidence and idle complete SQL inventory; it does not qualify a deployed provider or allow activation. Neon currently lacks the stronger provider guarantee. Names, catalog fingerprints and ownership tokens remain private.
 */
export type ManagedPostgresCutoverWriterIsolation = {
  state: 'pending' | 'unsupported' | 'unavailable' | 'blocked' | 'confirmed' | 'expired' | 'inactive';
  confirmed: boolean;
  inventory_available: boolean;
  /**
   * The complete observed catalog matches its immutable original fingerprint; this is not a catalog lock.
   */
  catalog_matches: boolean;
  /**
   * Fresh provider evidence attests a durable catalog guard.
   */
  catalog_stable: boolean;
  /**
   * Fresh provider evidence covers background and replication write paths across all dataset endpoints.
   */
  background_writers_fenced: boolean;
  client_sessions: number;
  background_workers: number;
  replication_workers: number;
  /**
   * Backends whose type is hidden by privileges or not recognized; these prevent confirmation.
   */
  unknown_backends: number;
  expires_at?: string;
};

