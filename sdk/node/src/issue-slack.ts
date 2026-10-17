import { createHash } from 'node:crypto';
import { verifyWebhook, type WebhookHeaders, type VerifyWebhookOptions, type VerifiedWebhook } from './webhook.js';

export const ISSUE_SLACK_DEFAULT_EVENTS = Object.freeze(['issue.created', 'issue.regressed', 'issue.impact_threshold_reached'] as const);
export const ISSUE_SLACK_EVENTS = Object.freeze([...ISSUE_SLACK_DEFAULT_EVENTS, 'issue.frequency_threshold_reached', 'issue.assigned', 'issue.resolved', 'issue.reopened', 'issue.ignored'] as const);
export type IssueSlackEvent = typeof ISSUE_SLACK_EVENTS[number];

export interface IssueSlackOptions {
  /** Bind this receiver to one app, even if a signing secret is reused. */
  appID: string;
  appSlug: string;
  dashboardOrigin: string;
  /** Optional current-team filter; it does not resolve Slack users or channels. */
  teamID?: string;
  events?: readonly IssueSlackEvent[];
}
export interface IssueSlackMessage {
  text: string;
  blocks: Array<{
    type: 'section';
    text: { type: 'plain_text'; text: string; emoji: false };
    accessory: { type: 'button'; text: { type: 'plain_text'; text: string; emoji: false }; url: string };
  }>;
}
export interface VerifiedIssueSlackMessage extends VerifiedWebhook {
  /** null means a verified delivery outside the selected app/team/events. */
  message: IssueSlackMessage | null;
  /** Stable across delivery attempt changes and JSON/CloudEvents wrappers. */
  transitionSHA256: string | null;
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
function validID(value: unknown): value is string {
  return typeof value === 'string' && uuid.test(value) && value !== '00000000-0000-0000-0000-000000000000';
}
function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new TypeError('invalid issue webhook');
  return value as Record<string, unknown>;
}
function clip(value: string, max: number): string {
  const points = Array.from(value.replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, ''));
  const cleaned = points.join('');
  if (cleaned.length <= max) return cleaned;
  let clipped = '';
  for (const point of points) { if (clipped.length + point.length > max - 1) break; clipped += point; }
  return clipped + '…';
}
function slackEscape(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

function delivery(payload: unknown): { type: string; transition: unknown; id?: unknown; appID?: unknown } {
  const root = object(payload);
  if (root.specversion !== undefined) {
    if (root.specversion !== '1.0' || typeof root.type !== 'string') throw new TypeError('invalid issue webhook');
    return { type: root.type, transition: root.data, id: root.id, appID: root.appid };
  }
  if (root.payload !== undefined) {
    const nested = object(root.payload);
    if (typeof nested.event !== 'string' || root.rule_name !== undefined && root.rule_name !== nested.event
      || nested.delivery_id !== undefined && nested.delivery_id !== root.id) throw new TypeError('invalid issue webhook');
    return { type: nested.event, transition: nested.data, id: root.id, appID: root.app_id };
  }
  // The formatter also accepts a logical issue transition directly.
  if (typeof root.type !== 'string') throw new TypeError('invalid issue webhook');
  return { type: root.type, transition: root };
}
function canonicalJSON(value: unknown, depth = 0): string {
  if (depth > 64) throw new TypeError('invalid issue webhook');
  if (Array.isArray(value)) return '[' + value.map(item => canonicalJSON(item, depth + 1)).join(',') + ']';
  if (value !== null && typeof value === 'object') {
    const row = value as Record<string, unknown>;
    return '{' + Object.keys(row).sort().map(key => JSON.stringify(key) + ':' + canonicalJSON(row[key], depth + 1)).join(',') + '}';
  }
  const encoded = JSON.stringify(value);
  if (encoded === undefined) throw new TypeError('invalid issue webhook');
  return encoded;
}

/** Format metadata only: no stack traces, raw tags, request context or customer IDs. */
export function formatIssueSlackMessage(payload: unknown, options: IssueSlackOptions): IssueSlackMessage | null {
  let origin: URL;
  try { origin = new URL(options.dashboardOrigin); } catch { throw new TypeError('invalid issue Slack options'); }
  if (!validID(options.appID) || !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(options.appSlug)
    || origin.protocol !== 'https:' || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash
    || (options.teamID !== undefined && !validID(options.teamID))) throw new TypeError('invalid issue Slack options');
  const events = options.events ?? ISSUE_SLACK_DEFAULT_EVENTS;
  if (!Array.isArray(events) || events.some(event => !ISSUE_SLACK_EVENTS.includes(event as IssueSlackEvent))) throw new TypeError('invalid issue Slack events');
  const wire = delivery(payload);
  if (!new Set<string>(events).has(wire.type)) return null;
  const envelope = object(wire.transition);
  if (envelope.type !== wire.type) throw new TypeError('invalid issue webhook');
  const issue = object(envelope.issue);
  if (!validID(envelope.id) || !validID(issue.id) || !validID(issue.app_id)
    || typeof issue.title !== 'string' || typeof issue.environment !== 'string'
    || typeof issue.state !== 'string' || !['open', 'resolved', 'ignored'].includes(issue.state)) throw new TypeError('invalid issue webhook');
  for (const owner of ['assignee_account_id', 'assignee_team_id']) {
    if (issue[owner] !== undefined && issue[owner] !== '' && !validID(issue[owner])) throw new TypeError('invalid issue webhook');
  }
  if (issue.assignee_account_id && issue.assignee_team_id) throw new TypeError('invalid issue webhook');
  if (wire.appID !== undefined && (!validID(wire.appID) || wire.appID.toLowerCase() !== issue.app_id.toLowerCase())) throw new TypeError('invalid issue webhook');
  if (issue.app_id.toLowerCase() !== options.appID.toLowerCase()
    || options.teamID !== undefined && String(issue.assignee_team_id).toLowerCase() !== options.teamID.toLowerCase()) return null;
  const labels: Record<IssueSlackEvent, string> = {
    'issue.created': 'New issue', 'issue.regressed': 'Issue regressed', 'issue.impact_threshold_reached': 'Customer-impact threshold reached',
    'issue.frequency_threshold_reached': 'Event-frequency threshold reached',
    'issue.assigned': 'Issue ownership changed', 'issue.resolved': 'Issue resolved', 'issue.reopened': 'Issue reopened', 'issue.ignored': 'Issue ignored',
  };
  const owner = issue.assignee_team_id ? `Team ${issue.assignee_team_id}` : issue.assignee_account_id ? `Account ${issue.assignee_account_id}` : 'Unassigned';
  const text = clip(`${labels[envelope.type as IssueSlackEvent]} · ${options.appSlug}\n${issue.environment} · ${issue.state}\n${issue.title}\nOwner: ${owner}`, 2900);
  const link = new URL(`/dashboard/apps/${options.appSlug}/issues`, origin);
  link.searchParams.set('issue', issue.id);
  return {
    text: slackEscape(text),
    blocks: [{ type: 'section', text: { type: 'plain_text', text, emoji: false },
      accessory: { type: 'button', text: { type: 'plain_text', text: 'View issue', emoji: false }, url: link.href } }],
  };
}

/** Verify exact received bytes before parsing or formatting the delivery. No network calls. */
export function issueSlackMessageFromWebhook(secret: string | Uint8Array, headers: WebhookHeaders, body: Uint8Array,
  options: IssueSlackOptions, verification: VerifyWebhookOptions = {}): VerifiedIssueSlackMessage {
  const verified = verifyWebhook(secret, headers, body, verification);
  let payload: unknown;
  try { payload = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(body)); } catch { throw new TypeError('invalid issue webhook'); }
  const wire = delivery(payload);
  if (wire.id !== verified.deliveryId) throw new TypeError('invalid issue webhook delivery ID');
  const message = formatIssueSlackMessage(payload, options);
  return { ...verified, message, transitionSHA256: message ? createHash('sha256').update(canonicalJSON(wire.transition)).digest('hex') : null };
}
