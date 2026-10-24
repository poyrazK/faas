/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PATCH /v1/orgs/{slug} body. Both fields are pointer-typed
 * so the handler distinguishes "omitted" (leave alone) from
 * "zero" (clear/empty). Authz routing:
 * - name → org.manage_billing (owner + billing roles)
 * - plan → org.change_plan (owner only)
 *
 */
export type PatchOrgRequest = {
  name?: string | null;
  plan?: 'free' | 'hobby' | 'pro' | 'scale';
};

