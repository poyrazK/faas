/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type RouteLifecycleHistoryApproval = {
  id: string;
  used: boolean;
  status: 'valid' | 'expired' | 'invalidated' | 'unavailable';
  status_reason: string;
  baseline_deployment_id: string;
  candidate_deployment_id: string;
  baseline_contract_sha256: string;
  candidate_contract_sha256: string;
  configuration_sha256: string;
  valid_until: string;
  invalidated_at?: string;
  graph_ids: Array<string>;
};

