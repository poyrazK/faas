/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Single read-only candidate row in the dry-run response. Mirrors the
 * create-edge-rule request body fields so the customer can
 * copy-paste the suggestion into the existing endpoint.
 * `kind` + `action` union shape matches the existing
 * `EdgeRule*Action` types.
 *
 */
export type EdgeRuleSuggestion = {
  /**
   * Operation path (e.g. `/users/{id}`).
   */
  path: string;
  /**
   * HTTP methods the suggestion applies to.
   */
  methods: Array<'get' | 'put' | 'post' | 'delete' | 'options' | 'head' | 'patch' | 'trace'>;
  /**
   * Edge-rule kind the suggestion produces.
   */
  kind: 'validate' | 'cors' | 'throttle' | 'jwt' | 'headers' | 'cache' | 'redirect' | 'rewrite';
  /**
   * Action payload (matches EdgeRule*Action types).
   */
  action: Record<string, any>;
};

