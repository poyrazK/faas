import { createHash, createHmac } from 'node:crypto';

/** Header naming the end user of an AI gateway call; it attributes and limits the call (ADR-994). */
export const AI_USER_HEADER = 'X-Gregale-AI-User';

/** Header naming the conversation of an AI gateway call; pooled provider keys stick to it (ADR-994). */
export const AI_SESSION_HEADER = 'X-Gregale-AI-Session';

/** Header naming up to five `key=value` tags of an AI gateway call (ADR-1436). */
export const AI_TAGS_HEADER = 'X-Gregale-AI-Tags';

/** Header naming the agent task, or trace, an AI gateway call belongs to (ADR 20261020202035). */
export const AI_TRACE_HEADER = 'X-Gregale-AI-Trace';

/** Response header carrying a call's ID, which feedback names (ADR 20261021171712). */
export const AI_CALL_HEADER = 'X-Gregale-AI-Call';

const AI_ID_PATTERN = /^[A-Za-z0-9._:@+=/|-]{1,64}$/;
const AI_TAG_KEY_PATTERN = /^[a-z][a-z0-9_.-]{0,31}$/;
const AI_TAG_VALUE_PATTERN = /^[A-Za-z0-9._:@+/-]{1,64}$/;
const AI_FEEDBACK_LABEL_PATTERN = /^[a-z][a-z0-9_.-]{0,31}$/;
const AI_TAGS_MAX = 5;

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

/** The end user, conversation, tags and trace of one AI call. */
export interface AIGatewayCaller {
  user?: string;
  session?: string;
  /** Up to five tags, such as `{ feature: 'search' }`, to see and cap cost by. */
  tags?: Record<string, string>;
  /** The agent task the call belongs to; calls of one trace are seen and limited together. */
  trace?: string;
}

/** Headers naming a call's end user, conversation, tags and trace. Throws on a value the gateway would reject. */
export function aiGatewayHeaders(caller: AIGatewayCaller): Record<string, string> {
  const headers: Record<string, string> = {};
  for (const [name, value] of [[AI_USER_HEADER, caller.user], [AI_SESSION_HEADER, caller.session]] as const) {
    if (value === undefined) continue;
    if (!isValidAIUserId(value)) {
      throw new AIGatewayError(`${name} must be 1 to 64 letters, digits or . _ : @ + = / | -; use opaqueAIUserId for raw identifiers`);
    }
    headers[name] = value;
  }
  if (caller.trace !== undefined) {
    if (!AI_ID_PATTERN.test(caller.trace)) throw new AIGatewayError(`${AI_TRACE_HEADER} must be 1 to 64 letters, digits or . _ : @ + = / | -`);
    headers[AI_TRACE_HEADER] = caller.trace;
  }
  if (caller.tags !== undefined) {
    const entries = Object.entries(caller.tags);
    if (entries.length === 0 || entries.length > AI_TAGS_MAX) throw new AIGatewayError(`${AI_TAGS_HEADER} takes 1 to ${AI_TAGS_MAX} tags`);
    for (const [key, value] of entries) {
      if (!AI_TAG_KEY_PATTERN.test(key) || !AI_TAG_VALUE_PATTERN.test(value)) {
        throw new AIGatewayError(
          `${AI_TAGS_HEADER} keys are 1 to 32 lowercase letters, digits, _ . or - starting with a letter; values 1 to 64 letters, digits or . _ : @ + / -`,
        );
      }
    }
    headers[AI_TAGS_HEADER] = entries.map(([key, value]) => `${key}=${value}`).join(',');
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

/** One rating of an AI gateway call. */
export interface AIGatewayFeedback {
  /** The integration the call went through. */
  integration: string;
  /** The call's ID, from its `X-Gregale-AI-Call` response header. */
  callId: string;
  rating: 'good' | 'bad';
  /** An optional short key, such as `wrong_answer`. */
  label?: string;
  /** Environment to read GREGALE_OUTBOUND_URL from; defaults to process.env. */
  env?: Record<string, string | undefined>;
  fetch?: typeof fetch;
}

/**
 * Rate one of the app's AI gateway calls (ADR 20261021171712). The
 * integration needs its request log on. A later rating of the same call
 * replaces the earlier one.
 */
export async function aiGatewayFeedback(feedback: AIGatewayFeedback): Promise<void> {
  if (feedback.rating !== 'good' && feedback.rating !== 'bad') throw new AIGatewayError('rating must be good or bad');
  if (feedback.label !== undefined && !AI_FEEDBACK_LABEL_PATTERN.test(feedback.label)) {
    throw new AIGatewayError('label must be 1 to 32 lowercase letters, digits, _ . or - starting with a letter');
  }
  if (!feedback.callId) throw new AIGatewayError('callId is the X-Gregale-AI-Call response header');
  const url = `${aiGatewayURL({ integration: feedback.integration }, { env: feedback.env })}/_gregale/feedback`;
  const body: Record<string, string> = { call_id: feedback.callId, rating: feedback.rating };
  if (feedback.label !== undefined) body.label = feedback.label;
  const response = await (feedback.fetch ?? fetch)(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new AIGatewayError(`feedback was refused with status ${response.status}`);
}
