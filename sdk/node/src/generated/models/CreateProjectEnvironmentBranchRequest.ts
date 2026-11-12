/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A new branch of the environment named in the path.
 */
export type CreateProjectEnvironmentBranchRequest = {
  /**
   * Slug of the new environment.
   */
  slug: string;
  /**
   * Time to live, a duration such as 24h between 1h and 720h. Defaults to 168h.
   */
  ttl?: string;
  /**
   * Outbound traffic from the branch. live (the default) behaves like a stage. contained reaches only the branch's own platform services (service calls inside the branch, the branch's managed PostgreSQL copies and the platform resolver) and takes no host-mediated action; it reaches the branch's own buckets through the platform's object relay, and a parent with object storage is refused where the operator has not enabled the relay. contained is available only where the operator enabled it. simulated is contained, and the branch can seed rehearsal members (POST /v1/dev/sessions/{project}/seed) whose side effects are simulated and recorded.
   */
  egress?: 'live' | 'contained' | 'simulated';
  /**
   * Whether the branch's instances join the private networks their workloads are attached to. off (the default) keeps them off, so a call to a private peer fails rather than reaching production. join attaches them with addresses of their own, never a workload's service address, so they can reach the parent's private peers; only a live branch may join.
   */
  private_networks?: 'off' | 'join';
};

