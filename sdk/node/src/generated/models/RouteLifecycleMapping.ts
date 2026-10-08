/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type RouteLifecycleMapping = {
  method: 'GET' | 'PUT' | 'POST' | 'DELETE' | 'OPTIONS' | 'HEAD' | 'PATCH' | 'TRACE';
  path: string;
  /**
   * Exact candidate x-gregale-successor HTTPS URL on the successor app's canonical host or verified application-wide custom domain, with no port, query or fragment. Ambiguous routing and tenant surfaces require further review.
   */
  successor_url: string;
  successor_method: 'GET' | 'PUT' | 'POST' | 'DELETE' | 'OPTIONS' | 'HEAD' | 'PATCH' | 'TRACE';
  successor_path: string;
  /**
   * Same-account destination app. Supply together with successor_deployment_id and successor_contract_sha256; omit all three for the source app candidate.
   */
  successor_app_id?: string;
  /**
   * Destination deployment. Legacy apps require one production destination at 100 percent. Project apps require one live active production graph member with frozen settings. Source-app successors must use the candidate.
   */
  successor_deployment_id?: string;
  /**
   * Authoritative destination doc_sha256 capture metadata.
   */
  successor_contract_sha256?: string;
};

