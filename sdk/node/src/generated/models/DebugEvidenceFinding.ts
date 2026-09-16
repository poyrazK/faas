/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One bounded, evidence-backed debugger finding.
 */
export type DebugEvidenceFinding = {
  code: string;
  title: string;
  detail: string;
  confidence: 'high' | 'medium' | 'low';
  evidence_refs?: Array<string>;
};

