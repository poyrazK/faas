/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Managed environment prefix to required canary mode in the selected scope. Prefixes match ^[A-Z][A-Z0-9_]{0,47}$. read covers listing; read_write requires byte transfer; write and read_write require successful cleanup. No implicit probes or writes.
 */
export type ObjectStorageReleaseRequirements = Record<string, 'read' | 'write' | 'read_write'>;
