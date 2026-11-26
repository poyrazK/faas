/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A service's cross-app grants from gregale.yaml: what its workload token may call on other apps of the same account.
 */
export type WorkloadUses = {
  queues?: Array<{
    /**
     * Project service name, app slug, or self.
     */
    app: string;
    /**
     * Queue name, or "*" for any.
     */
    queue?: string;
  }>;
  realtime?: Array<{
    app: string;
    /**
     * Channel glob with one trailing *.
     */
    channels?: string;
  }>;
  /**
   * Apps the service may schedule delayed tasks on.
   */
  delayed_tasks?: Array<string>;
  buckets?: Array<{
    bucket: string;
    /**
     * Owning app (default self).
     */
    app?: string;
    /**
     * Key prefix the grant is limited to.
     */
    prefix?: string;
    access?: 'read' | 'write' | 'read_write';
  }>;
};

