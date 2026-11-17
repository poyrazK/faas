/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolGrantCredential } from './ToolGrantCredential.js';
import type { ToolGrantIntent } from './ToolGrantIntent.js';
/**
 * Owner-readable retained delegation with frozen application/project identities, optional credential binding and irreversible revocation.
 */
export type ToolGrantRecord = {
  credential?: ToolGrantCredential;
  id: string;
  account_id: string;
  app: string;
  app_id: string;
  project_id?: string;
  intent: ToolGrantIntent;
  created_at: string;
  revoked_at?: string;
};

