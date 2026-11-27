import { createHash, randomUUID } from 'node:crypto';
import { names } from '../scripts/manifests.mjs';
import { inspectReportExport, regionalReportArguments, reportDeliveryRequest } from './bridge.mjs';

export const REPORT_UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const labels = ['north', 'south', 'merge'] as const;
type Label = typeof labels[number];
export type ReportOrder = { order_id: string; region: 'north' | 'south'; quantity: number; unit_price_cents: number };
export type ReportSummary = { currency: 'USD'; orders: number; units: number; revenue_cents: number };
export type ReportDelivery = { id: string; status: string; receipt_url: string };
type StoredReport = { summary: ReportSummary; delivery: ReportDelivery };
type DeliveryIntent = { route: string; summary: ReportSummary; request: ReturnType<typeof reportDeliveryRequest> };
export type ReportRecovery = { runSteps: Label[]; repeatSteps: Label[]; reuseSteps: Label[]; childRequestID?: string };
export type ReportRerun = { requestID: string; repeatSteps: readonly string[] };
export type RegionalReportStatus =
  | { requestID: string; status: 'staging' | 'queued' | 'running' | 'delivering' | 'rerun_unavailable' }
  | { requestID: string; status: 'failed'; outcome: 'failed' | 'cancelled' | 'deadline_exceeded'; recovery?: ReportRecovery }
  | { requestID: string; status: 'succeeded'; summary: ReportSummary };

type StepProgress = { label: Label; state: string; reason?: string; attempt?: number };
type Event = { sequence: number; type: 'pipeline'; status: string } |
  { sequence: number; type: 'step'; step: StepProgress };
export type ReportPipelineProgress = {
  nextSequence: number; gap: boolean; terminal: boolean; events: Event[];
  snapshot?: { status: string; steps: StepProgress[] };
};

type Manifest = {
  workflow_id: string;
  steps: { label: Label; depends_on?: Label[] }[];
};
type Completion = {
  completion_id: string; workflow_id: string;
  outcome: PipelineFinished['outcome'];
  steps: { label: Label; outcome: string; attempts: unknown[] | null; reused_run_id?: string }[];
};
type Managed = {
  id: string; workflow_id: string; status: 'queued' | 'running' | 'succeeded' | 'failed';
  pipeline?: { completion?: Completion; rerun?: { parent_id: string }; progress?: { status: string } };
};
export type RegionalPipeline = {
  thread_id: string; workflow_id: string; pipeline_id: string | null; original: Manifest;
  parent_thread_id: string | null; repeat_steps: Label[]; rerun_rejected: boolean;
  report: StoredReport | null; delivery_intent: DeliveryIntent | null; background_delivery: boolean;
};
export type PipelineFinished = {
  event_id: string; workflow_id: string; outcome: 'succeeded' | 'failed' | 'cancelled' | 'deadline_exceeded';
};
type DB = { query(sql: string, params: string[]): Promise<{ rows: unknown[] }> };
type Owned = { sha256: string; regional: RegionalPipeline | null };
type Request = <T>(path: string, options?: { method?: 'GET' | 'POST'; body?: object }) => Promise<T>;

export class ReportBackendError extends Error {
  status: number;
  constructor(status: number) {
    super(`Report backend request failed (${status})`);
    this.status = status;
  }
}

function hash(intent: unknown) {
  return `sha256:${createHash('sha256').update(JSON.stringify(intent)).digest('hex')}`;
}

function approvals(steps: readonly string[]) {
  if (!Array.isArray(steps) || steps.length > labels.length ||
      steps.some(step => !labels.includes(step as Label)) || new Set(steps).size !== steps.length) {
    throw new Error('Provide the exact report branches approved for repetition');
  }
  return [...steps].sort() as Label[];
}

function recoveryPlan(original: Manifest, completion: Completion): ReportRecovery {
  const steps = new Map(completion.steps.map(step => [step.label, step]));
  if (steps.size !== original.steps.length || completion.steps.length !== steps.size) {
    throw new Error('Invalid regional pipeline completion');
  }
  const affected = new Set<Label>();
  const repeatSteps: Label[] = [], reuseSteps: Label[] = [];
  // The fixed DAG declares dependencies before the merge.
  for (const { label, depends_on } of original.steps) {
    const step = steps.get(label);
    if (!step) throw new Error('Invalid regional pipeline completion');
    if (step.outcome !== 'succeeded' || depends_on?.some(dep => affected.has(dep))) {
      affected.add(label);
      if (step.attempts?.length || step.reused_run_id) repeatSteps.push(label);
    } else {
      reuseSteps.push(label);
    }
  }
  return { runSteps: [...affected], repeatSteps: repeatSteps.sort(), reuseSteps };
}

const pipelineStates = ['queued', 'running', 'succeeded', 'failed', 'cancelled', 'deadline_exceeded'];
const stepStates = [...pipelineStates, 'restoring', 'timed_out', 'out_of_memory', 'retrying', 'blocked', 'contract_failed', 'reused'];
const reasons = ['pending', 'dependencies', 'parallel_limit', 'capacity', 'budget_concurrency', 'retry_backoff',
  'admissions_disabled', 'admission_refused', 'fail_fast', 'plan_identity', 'cancelling'];

function projectStep(step: StepProgress): StepProgress {
  if (!labels.includes(step?.label) || !stepStates.includes(step.state)) throw new Error('Invalid report branch progress');
  const { label, state, reason, attempt } = step;
  return { label, state, ...(reason && reasons.includes(reason) ? { reason } : {}),
    ...(attempt !== undefined && Number.isSafeInteger(attempt) && attempt > 0 ? { attempt } : {}) };
}

export function createRegionalReports({ db, find, request, prefix, deliveries, endpointID, backgroundDelivery }: {
  db: DB; find(key: string[]): Promise<Owned | undefined>; request: Request; prefix: string; deliveries: string;
  endpointID: string; backgroundDelivery: boolean;
}) {
  async function reserve(key: string[], intent: string, original: Manifest, parent: RegionalPipeline | null, repeat: Label[]) {
    const thread = randomUUID();
    const workflowID = `report-${thread}`;
    const manifest = parent ? original : { ...original, workflow_id: workflowID };
    try {
      await db.query(
        `WITH owned AS (
           INSERT INTO report_requests (endpoint_id, customer_id, request_id, thread_id, sha256)
           VALUES ($1, $2, $3, $4, $5) ON CONFLICT (endpoint_id, customer_id, request_id) DO NOTHING
           RETURNING thread_id
         )
         INSERT INTO report_regional_pipelines (thread_id, workflow_id, original, parent_thread_id, repeat_steps, background_delivery)
         SELECT thread_id, $6, $7::jsonb, NULLIF($8, '')::uuid, $9::jsonb, $10::boolean FROM owned`,
        [...key, thread, intent, workflowID, JSON.stringify(manifest), parent?.thread_id ?? '', JSON.stringify(repeat), String(backgroundDelivery)]);
    } catch (error) {
      if ((error as { constraint?: string }).constraint === 'report_regional_one_child') {
        throw new Error('This report already has a rerun. Reuse its request UUID');
      }
      throw error;
    }
    const reserved = await find(key);
    if (!reserved?.regional || reserved.sha256 !== intent || reserved.regional.parent_thread_id !== (parent?.thread_id ?? null)) {
      throw new Error('Report request ID already belongs to different work');
    }
    return reserved.regional;
  }

  async function dispatch(row: RegionalPipeline, parent?: RegionalPipeline) {
    if (row.pipeline_id) return;
    let accepted;
    try {
      accepted = parent
        ? await request<Managed>(`/v2/execution-pipelines/${parent.pipeline_id}/reruns`, { body: {
            workflow_id: row.workflow_id, original: row.original, repeat_steps: row.repeat_steps,
          } })
        : await request<Managed>('/v2/execution-workflows', { body: row.original });
    } catch (error) {
      if (!parent || !(error instanceof ReportBackendError) || error.status !== 409) throw error;
      const { rows } = await db.query(
        `UPDATE report_regional_pipelines SET rerun_rejected=(pipeline_id IS NULL)
          WHERE thread_id=$1 RETURNING pipeline_id, rerun_rejected`, [row.thread_id]);
      const saved = rows[0] as Pick<RegionalPipeline, 'pipeline_id' | 'rerun_rejected'> | undefined;
      if (!saved) throw new Error('Report rerun disappeared during admission');
      row.pipeline_id = saved.pipeline_id;
      row.rerun_rejected = saved.rerun_rejected;
      return;
    }
    if (!REPORT_UUID.test(accepted.id) || accepted.workflow_id !== row.workflow_id ||
        (parent && accepted.pipeline?.rerun?.parent_id !== parent.pipeline_id)) throw new Error('Invalid admitted report pipeline');
    await rememberPipeline(row, accepted.id);
  }

  async function rememberPipeline(row: RegionalPipeline, id: string) {
    const { rows } = await db.query(
      `UPDATE report_regional_pipelines SET pipeline_id=$2, rerun_rejected=false
        WHERE thread_id=$1 AND (pipeline_id IS NULL OR pipeline_id=$2) RETURNING pipeline_id`, [row.thread_id, id]);
    if (rows.length !== 1) throw new Error('Report pipeline identity changed');
    row.pipeline_id = id;
    row.rerun_rejected = false;
  }

  async function receipt(row: RegionalPipeline, id = row.pipeline_id) {
    const data = await request<{ managed?: Managed[] }>(`/v1/execution-workflows/${row.workflow_id}`, { method: 'GET' });
    const run = data.managed?.find(item => item.id === id && item.workflow_id === row.workflow_id);
    const done = run?.pipeline?.completion;
    if (!run || (done && (done.completion_id !== run.id || done.workflow_id !== row.workflow_id ||
        !['succeeded', 'failed', 'cancelled', 'deadline_exceeded'].includes(done.outcome)))) {
      throw new Error('Invalid regional pipeline receipt');
    }
    return { run, done };
  }

  async function planDelivery(row: RegionalPipeline, done: Completion): Promise<DeliveryIntent> {
    const output = await inspectReportExport(done, (id: string) => {
      if (!REPORT_UUID.test(id)) throw new Error('Invalid report Run identity');
      return request(`/v1/executions/${id}`, { method: 'GET' });
    });
    const { currency, orders, units, revenue_cents } = output.summary;
    const intent = { route: deliveries, summary: { currency, orders, units, revenue_cents },
      request: reportDeliveryRequest(done.completion_id, output) };
    const { rows } = await db.query(
      `UPDATE report_regional_pipelines SET delivery_intent=COALESCE(delivery_intent,$3::jsonb)
        WHERE thread_id=$1 AND pipeline_id=$2 RETURNING delivery_intent`,
      [row.thread_id, done.completion_id, JSON.stringify(intent)]);
    const saved = (rows[0] as { delivery_intent: DeliveryIntent } | undefined)?.delivery_intent;
    if (!saved) throw new Error('Report disappeared during delivery');
    return saved;
  }

  async function deliver(row: RegionalPipeline, intent: DeliveryIntent): Promise<StoredReport | null> {
    if (intent.route !== deliveries) throw new Error('Private report delivery route changed');
    const delivery = await request<ReportDelivery & { object?: object }>(deliveries, { body: intent.request });
    if (!REPORT_UUID.test(delivery.id) || delivery.receipt_url !== `${deliveries}/${delivery.id}`) {
      throw new Error('Invalid private delivery receipt');
    }
    if (['pending', 'copying'].includes(delivery.status)) return null;
    if (delivery.status !== 'completed' || !delivery.object) throw new Error('Private report delivery failed');
    const stored: StoredReport = { summary: intent.summary,
      delivery: { id: delivery.id, status: delivery.status, receipt_url: delivery.receipt_url } };
    const { rows } = await db.query(
      `UPDATE report_regional_pipelines SET report=COALESCE(report,$2::jsonb)
        WHERE thread_id=$1 RETURNING report`, [row.thread_id, JSON.stringify(stored)]);
    const saved = (rows[0] as { report: StoredReport } | undefined)?.report;
    if (!saved || saved.delivery.id !== delivery.id) throw new Error('Private report receipt changed');
    return saved;
  }

  async function status(requestID: string, row: RegionalPipeline): Promise<RegionalReportStatus> {
    if (!row.pipeline_id) return { requestID, status: row.rerun_rejected ? 'rerun_unavailable' : 'staging' };
    if (row.report) return { requestID, status: 'succeeded', summary: row.report.summary };
    if (row.delivery_intent) {
      return row.background_delivery ? { requestID, status: 'delivering' } : deliveryStatus(requestID, row, row.delivery_intent);
    }
    const { run, done } = await receipt(row);
    if (!done) {
      if (run.status !== 'queued' && run.status !== 'running') throw new Error('Missing terminal report receipt');
      return { requestID, status: run.pipeline?.progress?.status === 'running' ? 'running' : run.status };
    }
    if (done.outcome === 'failed') {
      const recovery = recoveryPlan(row.original, done);
      const { rows } = await db.query(
        `SELECT child.request_id
           FROM report_regional_pipelines attempt
           JOIN report_requests child ON child.thread_id=attempt.thread_id
           JOIN report_requests parent ON parent.thread_id=attempt.parent_thread_id
             AND parent.endpoint_id=child.endpoint_id AND parent.customer_id=child.customer_id
          WHERE attempt.parent_thread_id=$1`, [row.thread_id]);
      const child = rows[0] as { request_id: string } | undefined;
      if (child) recovery.childRequestID = child.request_id;
      return { requestID, status: 'failed', outcome: done.outcome, recovery };
    }
    if (done.outcome !== 'succeeded') return { requestID, status: 'failed', outcome: done.outcome };
    return row.background_delivery ? { requestID, status: 'delivering' } : deliveryStatus(requestID, row, await planDelivery(row, done));
  }

  async function deliveryStatus(requestID: string, row: RegionalPipeline, intent: DeliveryIntent): Promise<RegionalReportStatus> {
    row.report = await deliver(row, intent);
    return row.report ? { requestID, status: 'succeeded', summary: row.report.summary } : { requestID, status: 'delivering' };
  }

  async function complete(event: PipelineFinished): Promise<boolean> {
    const { rows } = await db.query(
      `SELECT to_jsonb(p) AS regional, parent.pipeline_id AS parent_pipeline_id
         FROM report_regional_pipelines p JOIN report_requests r ON r.thread_id=p.thread_id
         LEFT JOIN report_regional_pipelines parent ON parent.thread_id=p.parent_thread_id
        WHERE r.endpoint_id=$1 AND p.workflow_id=$2`, [endpointID, event.workflow_id]);
    const match = rows[0] as { regional: RegionalPipeline; parent_pipeline_id: string | null } | undefined;
    if (!match) return true;
    const { regional: row, parent_pipeline_id: parentID } = match;
    if (row.pipeline_id && row.pipeline_id !== event.event_id) throw new Error('Report completion identity changed');
    if ((row.report || row.delivery_intent) && event.outcome !== 'succeeded') throw new Error('Report completion outcome changed');
    if (row.report) return true;
    if (row.delivery_intent) return (await deliver(row, row.delivery_intent)) !== null;
    const { run, done } = await receipt(row, event.event_id);
    if (!done || done.outcome !== event.outcome ||
        (row.parent_thread_id ? !parentID || run.pipeline?.rerun?.parent_id !== parentID : !!run.pipeline?.rerun)) {
      throw new Error('Report completion does not match its receipt');
    }
    // The workflow reservation predates admission. Recover a linked pipeline
    // even if a successful admission response was lost before it was saved.
    if (!row.pipeline_id) await rememberPipeline(row, run.id);
    return done.outcome !== 'succeeded' || (await deliver(row, await planDelivery(row, done))) !== null;
  }

  return {
    status,
    complete,
    async submit(key: string[], orders: readonly ReportOrder[]) {
      const original = { ...regionalReportArguments(names(prefix), orders),
        workflow_id: '', version: 'v1', failure_policy: 'continue_independent' };
      const row = await reserve(key, hash(['regional', original]), original as Manifest, null, []);
      await dispatch(row);
      return status(key[2], row);
    },
    async rerun(parent: RegionalPipeline, child: string[], repeatSteps: readonly string[]) {
      if (!parent.pipeline_id) throw new Error('Wait for a failed regional report before rerunning');
      const repeat = approvals(repeatSteps);
      const intent = hash(['regional-rerun', parent.thread_id, repeat]);
      const previous = await find(child);
      if (!previous) {
        const { done } = await receipt(parent);
        if (done?.outcome !== 'failed') throw new Error('Only a failed regional report can be rerun');
        if (JSON.stringify(repeat) !== JSON.stringify(recoveryPlan(parent.original, done).repeatSteps)) {
          throw new Error('Approve exactly the branches listed by report recovery');
        }
      }
      const row = await reserve(child, intent, parent.original, parent, repeat);
      await dispatch(row, parent);
      return status(child[2], row);
    },
    async progress(row: RegionalPipeline, after: number): Promise<ReportPipelineProgress | null> {
      if (!row.pipeline_id) return null;
      const page = await request<{
        pipeline_id: string; next_sequence: number; gap?: boolean; terminal: boolean;
        events: (Event | null)[] | null; progress?: { status: string; steps: StepProgress[] };
      }>(`/v1/execution-pipelines/${row.pipeline_id}/events?after=${after}`, { method: 'GET' });
      if (page.pipeline_id !== row.pipeline_id || !Number.isSafeInteger(page.next_sequence) || page.next_sequence < after) {
        throw new Error('Invalid report progress page');
      }
      const events: Event[] = [];
      let sequence = after;
      for (const event of page.events ?? []) {
        if (!event || !Number.isSafeInteger(event.sequence) || event.sequence <= sequence || event.sequence > page.next_sequence) {
          throw new Error('Invalid report progress sequence');
        }
        sequence = event.sequence;
        if (event.type === 'step') events.push({ sequence, type: 'step', step: projectStep(event.step) });
        else if (event.type === 'pipeline' && pipelineStates.includes(event.status)) events.push({ sequence, type: 'pipeline', status: event.status });
        else throw new Error('Invalid report progress event');
      }
      const snapshot = page.progress;
      if (snapshot && !pipelineStates.includes(snapshot.status)) throw new Error('Invalid report progress status');
      return { nextSequence: page.next_sequence, gap: page.gap === true, terminal: page.terminal === true, events,
        ...(snapshot ? { snapshot: { status: snapshot.status, steps: snapshot.steps.map(projectStep) } } : {}) };
    },
  };
}
