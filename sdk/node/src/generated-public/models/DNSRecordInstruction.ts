/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One DNS record a customer publishes for a custom domain (ADR-520). `alternative` marks an A/AAAA routing record that replaces the CNAME where a CNAME is not allowed, such as at a zone apex.
 */
export type DNSRecordInstruction = {
  type: 'TXT' | 'CNAME' | 'A' | 'AAAA';
  name: string;
  value: string;
  purpose: 'verification' | 'routing';
  alternative?: boolean;
};

