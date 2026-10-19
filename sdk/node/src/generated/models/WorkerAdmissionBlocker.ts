/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Scheduler-owned admission evidence. Codes currently include account_worker_limit, concurrency_limit, node_ram, node_vcpu, node_cpu, no_admitting_nodes, node_ports, placement_changed, spend_cap, retry_cooldown, admission_refused and deployment_unavailable. Node constraints omit host identities and fleet usage. Values for account and concurrency budgets describe the actual decision, including residents still draining.
 */
export type WorkerAdmissionBlocker = {
  code: string;
  observed_at: string;
  /**
   * Whether this refusal observation is older than fifteen minutes, independently of the pool counts.
   */
  stale?: boolean;
  /**
   * Units for numeric evidence, such as replicas, ram_mb, vcpu, cpu_millicores or cents.
   */
  resource?: string;
  requested?: number;
  used?: number;
  limit?: number;
  /**
   * App-wide admission cooldown deadline when code is retry_cooldown.
   */
  retry_at?: string;
};

