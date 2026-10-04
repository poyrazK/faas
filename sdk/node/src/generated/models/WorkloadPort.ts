/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One protocol-aware listener in a container workload (ADR-165).
 */
export type WorkloadPort = {
  name?: string;
  port: number;
  protocol: 'tcp' | 'udp';
  /**
   * Internal-only listener: never public; same-account services reach it at the private service address (ADR-530).
   */
  internal?: boolean;
};

