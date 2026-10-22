import { createHash, createHmac } from 'node:crypto';

/** Header naming the end user of an AI gateway call; it attributes and limits the call (ADR-994). */
export const AI_USER_HEADER = 'X-Gregale-AI-User';

/** Header naming the conversation of an AI gateway call; pooled provider keys stick to it (ADR-994). */
export const AI_SESSION_HEADER = 'X-Gregale-AI-Session';

const AI_ID_PATTERN = /^[A-Za-z0-9._:@+=/|-]{1,64}$/;

/** A helper argument the gateway would reject. Messages never include header values. */
export class AIGatewayError extends Error {
  constructor(message: string) {
    super(`ai-gateway: ${message}`);
    this.name = 'AIGatewayError';
  }
}

/** Whether a value is accepted as an X-Gregale-AI-User or X-Gregale-AI-Session ID. */
export function isValidAIUserId(value: string): boolean {
  return AI_ID_PATTERN.test(value);
}

/** One integration (`/i/{id}`) or one AI route (`/r/{id}`). */
export type AIGatewayTarget = { integration: string; route?: never } | { route: string; integration?: never };

export interface AIGatewayURLOptions {
  /** Environment to read GREGALE_OUTBOUND_URL from; defaults to process.env. */
  env?: Record<string, string | undefined>;
}

/**
 * Base URL of an integration or route on the app's outbound relay. Append
 * the provider path, e.g. `${aiGatewayURL({ route })}/v1` as an OpenAI
 * client's baseURL. Throws when the app has no GREGALE_OUTBOUND_URL.
 */
export function aiGatewayURL(target: AIGatewayTarget, options: AIGatewayURLOptions = {}): string {
  const env = options.env ?? process.env;
  const base = env.GREGALE_OUTBOUND_URL;
  if (!base) throw new AIGatewayError('GREGALE_OUTBOUND_URL is not set; the outbound relay is only available inside Gregale apps');
  const id = target.integration ?? target.route;
  if (!id || id.includes('/')) throw new AIGatewayError('target needs one integration or route ID');
  return `${base.replace(/\/+$/, '')}/${target.integration ? 'i' : 'r'}/${encodeURIComponent(id)}`;
}

/** The end user and conversation of one AI call. */
export interface AIGatewayCaller {
  user?: string;
  session?: string;
}

/** Headers naming a call's end user and conversation. Throws on a value the gateway would reject. */
export function aiGatewayHeaders(caller: AIGatewayCaller): Record<string, string> {
  const headers: Record<string, string> = {};
  for (const [name, value] of [[AI_USER_HEADER, caller.user], [AI_SESSION_HEADER, caller.session]] as const) {
    if (value === undefined) continue;
    if (!isValidAIUserId(value)) {
      throw new AIGatewayError(`${name} must be 1 to 64 letters, digits or . _ : @ + = / | -; use opaqueAIUserId for raw identifiers`);
    }
    headers[name] = value;
  }
  return headers;
}

/**
 * A stable, opaque end-user ID for a raw identifier such as an e-mail
 * address, so personal data never reaches the gateway's usage rows. With a
 * secret the ID is an HMAC and cannot be guessed back from the raw value.
 */
export function opaqueAIUserId(raw: string, secret?: string): string {
  const digest = secret === undefined ? createHash('sha256').update(raw) : createHmac('sha256', secret).update(raw);
  return `u_${digest.digest('base64url')}`;
}

/** Wrap fetch so every call carries the caller's AI gateway headers, e.g. as an OpenAI client's `fetch`. */
export function withAIGatewayHeaders(fetchImpl: typeof fetch, caller: AIGatewayCaller): typeof fetch {
  const fixed = aiGatewayHeaders(caller);
  return (input, init) => {
    const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
    for (const [name, value] of Object.entries(fixed)) headers.set(name, value);
    return fetchImpl(input, { ...init, headers });
  };
}
