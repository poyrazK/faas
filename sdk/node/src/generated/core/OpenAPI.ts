/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApiRequestOptions } from './ApiRequestOptions.js';
import type { CancelablePromise } from './CancelablePromise.js';

type Resolver<T> = (options: ApiRequestOptions) => Promise<T>;
type Headers = Record<string, string>;

export type OpenAPIConfig = {
  FETCH?: typeof fetch;
  REQUEST?: <T>(options: ApiRequestOptions) => CancelablePromise<T>;
  BASE: string;
  VERSION: string;
  WITH_CREDENTIALS: boolean;
  CREDENTIALS: 'include' | 'omit' | 'same-origin';
  TOKEN?: string | Resolver<string> | undefined;
  USERNAME?: string | Resolver<string> | undefined;
  PASSWORD?: string | Resolver<string> | undefined;
  HEADERS?: Headers | Resolver<Headers> | undefined;
  ENCODE_PATH?: ((path: string) => string) | undefined;
};

export const createOpenAPIConfig = (): OpenAPIConfig => ({
  BASE: 'https://api.gregale.dev',
  VERSION: '1.0.0',
  WITH_CREDENTIALS: false,
  CREDENTIALS: 'include',
  TOKEN: undefined,
  USERNAME: undefined,
  PASSWORD: undefined,
  HEADERS: undefined,
  ENCODE_PATH: undefined,
});

export const OpenAPI: OpenAPIConfig = createOpenAPIConfig();
