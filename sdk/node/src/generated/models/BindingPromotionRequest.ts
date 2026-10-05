/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Policy for a server-enforced bindings promotion; the gate is always required on this route.
 */
export type BindingPromotionRequest = {
  expected_serving_deployment_id?: string;
  /**
   * Positive Go duration; maximum age of passed verification evidence.
   */
  max_verification_age?: string;
  /**
   * Explicit queue/outbound connectivity probe waiver; coverage remains partial. Push consumer readiness and the independent 30-second poll window cannot be waived.
   */
  allow_unsupported?: boolean;
  /**
   * Require current managed-secret application receipts; use the dedicated promote-with-application-ack route for compatibility with older servers. The dedicated route always forces true.
   */
  require_application_ack?: boolean;
};

