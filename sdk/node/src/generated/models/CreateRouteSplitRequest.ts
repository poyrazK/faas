/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The split the route-resources read suggested, repeated so apid can refuse one that changed.
 */
export type CreateRouteSplitRequest = {
  kind: 'memory' | 'cpu';
  route: string;
  method?: string;
  route_ram_mb: number;
  main_ram_mb: number;
  /**
   * The analysed window, as for the route-resources read (default 24h).
   */
  since?: string;
};

