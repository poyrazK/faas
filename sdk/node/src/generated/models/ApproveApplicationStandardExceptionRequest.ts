/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Time-limited, reasoned exception for one adopted standard field, bound to the enrollment revision.
 */
export type ApproveApplicationStandardExceptionRequest = {
  expected_revision: number;
  standard_id: string;
  version: number;
  field: 'log_destinations' | 'require_signed' | 'security_policy' | 'trusted_publishers' | 'egress_cidrs' | 'egress_extra_ports';
  /**
   * Non-null value for the selected field, validated by the server against that field and the full inherited controls.
   */
  value: (boolean | 'off' | 'warn' | 'enforce' | Array<string> | Array<number>);
  /**
   * Trimmed nonempty UTF-8 reason bounded to 512 bytes.
   */
  reason: string;
  /**
   * Future expiry within 30 days of server time.
   */
  expires_at: string;
};

