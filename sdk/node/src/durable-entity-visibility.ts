import type {DurableEntityInspectResponse} from './generated/models/DurableEntityInspectResponse.js';
import {inspectDurableEntity, type DurableEntityInspectOptions} from './durable-entities.js';
import {durableEntityJSON} from './durable-entity-handler.js';

export interface DurableEntityFinding {code: string; detail: string}
export interface DurableEntityObservation {
  observed_at: string;
  inspection: DurableEntityInspectResponse;
  alarm_status: 'none' | 'scheduled' | 'due' | 'backoff' | 'exhausted';
  outbox_status: 'empty' | 'pending' | 'backoff' | 'exhausted';
  findings: DurableEntityFinding[];
  limitations: string[];
}
/** Pure diagnostics of a metadata observation; due labels use the local clock. */
export function summarizeDurableEntity(inspection: DurableEntityInspectResponse, now = new Date()): DurableEntityObservation {
  if (!Number.isFinite(now.getTime())) throw new TypeError('invalid observation clock');
  if (!Number.isSafeInteger(inspection.version) || inspection.version < 0) throw new RangeError('unsafe entity version');
  const input = JSON.parse(durableEntityJSON(inspection)) as DurableEntityInspectResponse;
  const report: DurableEntityObservation = {observed_at: now.toISOString(), inspection: input, alarm_status: 'none', outbox_status: 'empty', findings: [], limitations: [
    'Metadata observation; ownership and lease status are not exposed.',
    'Replay receipts and application state are not exposed; retain invocation IDs and results.',
    'Head delivery is a later transport observation, not atomic with entity inspection; unknown does not mean never accepted.',
  ]};
  const add = (code: string, detail: string) => {report.findings.push({code, detail});};
  const future = (value: string | undefined) => {
    if (value === undefined) return false;
    const time = Date.parse(value); if (!Number.isFinite(time)) throw new TypeError('invalid observed deadline');
    return time > now.getTime();
  };
  if (!input.state_committed) add('state_uncommitted', 'No business transition has committed in this scope.');
  if (input.alarm.alarm_at !== undefined) {
    if (input.alarm.exhausted) {
      report.alarm_status = 'exhausted';
      add('alarm_exhausted', 'Alarm attempt budget is exhausted. Inspect the handler failure before an explicit fenced retry.');
    } else if (future(input.alarm.alarm_at)) report.alarm_status = 'scheduled';
    else if (future(input.alarm.next_attempt_at)) report.alarm_status = 'backoff';
    else {report.alarm_status = 'due'; add('alarm_due', 'Alarm is due by the local clock; this alone does not establish a stalled worker.');}
  }
  if (input.outbox.pending > 0) {
    if (input.outbox.exhausted) {
      report.outbox_status = 'exhausted';
      add('outbox_exhausted', 'Outbox head attempt budget is exhausted. Check webhook configuration and retained delivery history before an explicit fenced retry.');
    } else report.outbox_status = future(input.outbox.next_attempt_at) ? 'backoff' : 'pending';
    switch (input.outbox.head_delivery?.status) {
      case undefined: case 'unknown':
        add('delivery_unknown', 'Head delivery history is unavailable or pruned; do not infer that no delivery occurred.'); break;
      case 'failed': case 'dead':
        add('delivery_failed', 'Retained head delivery reports failure; entity pending work is a separate observation.'); break;
      case 'succeeded':
        add('delivery_succeeded_pending', 'Retained head delivery succeeded while the entity head remains pending; acceptance and entity acknowledgement are separate steps.'); break;
      case 'pending': case 'in_flight': break;
      default: add('delivery_unrecognized', 'Head delivery returned an unrecognized status; treat transport outcome as unknown.');
    }
  }
  return report;
}
/** Read metadata only, then diagnose it using the observation clock. */
export async function observeDurableEntity(options: DurableEntityInspectOptions): Promise<DurableEntityObservation> {
  return summarizeDurableEntity(await inspectDurableEntity(options));
}
