/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * API key creation payload — label and optional scopes. Plaintext is returned exactly once in the 201 response. Scopes defaults to `["admin"]` when omitted so existing callers preserve the legacy full-access behavior. rev2.
 */
export type CreateKeyRequest = {
  label?: string;
  /**
   * Requested permission set. The server rejects unknown scopes. github:manage controls customer GitHub connection automation. Object-storage read/write scopes do not expose data until a storage manager grants the key access to a logical bucket.
   */
  scopes?: Array<'admin' | 'apps:read' | 'deploy:write' | 'secrets:read' | 'secrets:write' | 'usage:read' | 'runs:read' | 'runs:write' | 'env:read' | 'env:write' | 'registry_credentials:read' | 'registry_credentials:write' | 'upstreams:write' | 'metrics:write' | 'delayed_tasks:read' | 'delayed_tasks:write' | 'events:publish' | 'queues:send' | 'queues:consume' | 'storage:manage' | 'storage:read' | 'storage:write' | 'postgres:manage' | 'postgres:read' | 'project_environments:read' | 'project_environments:qualify' | 'github:manage'>;
  /**
   * Project slugs the key is limited to. Only an agent key (scope agent) can be limited; it then reaches only apps in these projects and cannot carry Postgres scopes. Omit for every project.
   */
  projects?: Array<string>;
  /**
   * Kinds of action an agent key must have a person approve before apid lets the request through. Omit for every kind; send an empty list for none. Only agent keys take this field.
   */
  approval_categories?: Array<'delete' | 'production' | 'credentials' | 'billing'>;
};

