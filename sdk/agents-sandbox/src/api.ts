// HTTP access to Gregale sandbox sessions (ADR-20261027090013 §3). The
// control plane (create, read, close, ports) takes the developer's API key;
// the data plane (exec, files, workspace) takes the session's own token,
// which a read of the session mints. Both live on the API host: the gateway
// routes data-plane paths to the session's node.

export const DEFAULT_API_BASE = 'https://api.gregale.dev';

/** A session as the API shows it. */
export type SandboxView = {
  id: string;
  state: 'queued' | 'starting' | 'ready' | 'frozen' | 'closed' | 'expired' | 'failed';
  kind: string;
  preset: string;
  ram_mb: number;
  network: { mode: string };
  expires_at: string;
  labels: Record<string, string>;
  outcome?: { code: string; message?: string };
  close_requested_at?: string;
  token?: string;
  token_expires_at?: string;
  snapshot_id?: string;
};

/** An exposed port as the API shows it. */
export type SandboxPortView = {
  port: number;
  visibility: 'private' | 'public';
  url: string;
  created_at: string;
  preview_token?: string;
  preview_token_expires_at?: string;
};

/** An RFC 7807 problem from the API. */
export type Problem = {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  code?: string;
  limit?: number;
  observed?: number;
};

/** A request the API refused. */
export class GregaleApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly problem: Problem;
  constructor(status: number, problem: Problem) {
    super(`${problem.title ?? 'Gregale API error'} (${status}${problem.code ? ` ${problem.code}` : ''})${problem.detail ? `: ${problem.detail}` : ''}`);
    this.name = 'GregaleApiError';
    this.status = status;
    this.code = problem.code ?? '';
    this.problem = problem;
  }
}

export type ApiOptions = {
  /** The developer's API key; GREGALE_API_KEY when unset. */
  apiKey?: string;
  /** The API's origin; GREGALE_API_BASE, then https://api.gregale.dev. */
  apiBase?: string;
  /** For tests and custom transports. */
  fetch?: typeof fetch;
};

type RequestArgs = {
  query?: Record<string, string | number | boolean | undefined>;
  json?: unknown;
  body?: Uint8Array;
  contentType?: string;
  accept?: string;
};

export class SandboxApi {
  readonly apiBase: string;
  readonly #apiKey: string;
  readonly #fetch: typeof fetch;

  constructor(options: ApiOptions = {}) {
    const apiKey = options.apiKey ?? process.env.GREGALE_API_KEY;
    if (!apiKey) {
      throw new Error('GregaleSandboxClient needs an API key: pass apiKey or set GREGALE_API_KEY');
    }
    this.#apiKey = apiKey;
    this.apiBase = (options.apiBase ?? process.env.GREGALE_API_BASE ?? DEFAULT_API_BASE).replace(/\/+$/, '');
    this.#fetch = options.fetch ?? fetch;
  }

  /** A control-plane call with the API key; answers the decoded JSON. */
  async control<T>(method: string, path: string, args: RequestArgs = {}): Promise<T> {
    const res = await this.send(method, path, this.#apiKey, args);
    return (res.status === 204 ? undefined : await res.json()) as T;
  }

  /** A data-plane call with a session token; answers the response. */
  async data(method: string, path: string, token: string, args: RequestArgs = {}): Promise<Response> {
    return await this.send(method, path, token, args);
  }

  private async send(method: string, path: string, bearer: string, args: RequestArgs): Promise<Response> {
    const url = new URL(this.apiBase + path);
    for (const [k, v] of Object.entries(args.query ?? {})) {
      if (v !== undefined) url.searchParams.set(k, String(v));
    }
    const headers: Record<string, string> = { Authorization: `Bearer ${bearer}`, Accept: args.accept ?? 'application/json' };
    let body: BodyInit | undefined;
    if (args.json !== undefined) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(args.json);
    } else if (args.body !== undefined) {
      headers['Content-Type'] = args.contentType ?? 'application/octet-stream';
      body = args.body;
    }
    const res = await this.#fetch(url, { method, headers, body });
    if (!res.ok) {
      let problem: Problem = { status: res.status };
      try {
        problem = (await res.json()) as Problem;
      } catch {
        // Not a problem document; the status says enough.
      }
      throw new GregaleApiError(res.status, problem);
    }
    return res;
  }
}
