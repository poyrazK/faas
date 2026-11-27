import { createHash, randomUUID } from 'node:crypto';
import { createReportCompletionReceiver } from './report-completion.ts';
import { createRegionalReports, ReportBackendError, REPORT_UUID as UUID } from './report-regional.ts';
import type { RegionalPipeline, RegionalReportStatus, ReportDelivery, ReportOrder,
  ReportPipelineProgress, ReportRerun, ReportSummary } from './report-regional.ts';
export type { ReportOrder, ReportRecovery, ReportRerun } from './report-regional.ts';

type Database = {
  query(sql: string, params: string[]): Promise<{ rows: unknown[] }>;
};

type IntakeOptions = {
  db: Database;
  apiURL: string;
  token: string;
  app: string;
  endpointID: string;
  uploadRoute: string;
  taskPrefix?: string;
  /** Mount receiveCompletion before enabling background delivery for submissions. */
  completionWebhookSecret?: string;
  fetch?: typeof fetch;
};

export type ReportKey = {
  /** Use the authenticated customer ID from the application session. */
  customerID: string;
  /** Generate once per submission and reuse on transport retries. */
  requestID: string;
};

type FailedOutcome = 'failed' | 'cancelled' | 'deadline_exceeded';
export type ReportStatus =
  | RegionalReportStatus
  | { requestID: string; status: 'intake_expired' };

export type ReportProgress = { report: ReportStatus; pipeline: ReportPipelineProgress | null };

export type ReportDownload = {
  url: string;
  method: 'GET';
  headers: Record<string, string>;
  expires_at: string;
};

type RequestRow = {
  thread_id: string;
  sha256: string;
  published_at: Date | null;
  grant_expires_at: Date | null;
  regional: RegionalPipeline | null;
};

type CompletedReport =
  | { outcome: FailedOutcome }
  | { outcome: 'succeeded'; summary: ReportSummary; delivery: ReportDelivery };

type Progress = { received: boolean; finished: boolean; report: CompletedReport | null };

const MAX_FILE_BYTES = 64 * 1024 * 1024;

/** One invocation per private thread. Keep this helper on the trusted backend. */
export function createReportIntake(options: IntakeOptions) {
  const { db, app, uploadRoute, token } = options;
  const endpointID = options.endpointID.toLowerCase();
  const prefix = options.taskPrefix ?? 'gr-demo';
  const taskName = `${prefix}-sales`;
  const send = options.fetch ?? globalThis.fetch;
  const api = new URL(options.apiURL);
  if (!UUID.test(endpointID) || !app || !uploadRoute || !token || !/^[a-z][a-z0-9-]{0,29}$/.test(prefix)) {
    throw new Error('Configure the private Report Agent backend');
  }
  const appPath = `/v1/apps/${encodeURIComponent(app)}`;
  const deliveries = `${appPath}/run-artifact-deliveries/${encodeURIComponent(uploadRoute)}`;
  const inbox = `${appPath}/realtime/endpoints/${endpointID}/channels/agent-inbox/retained-messages`;

  async function request<T>(path: string, { method = 'POST', body, file }: {
    method?: 'GET' | 'POST'; body?: object; file?: { bytes: Blob; sha256: string };
  } = {}): Promise<T> {
    const headers = new Headers({ Authorization: `Bearer ${token}` });
    if (file) {
      headers.set('content-type', 'application/octet-stream');
      headers.set('x-content-sha256', file.sha256);
    } else if (body) headers.set('content-type', 'application/json');
    const response = await send(new URL(path, api), {
      method: file ? 'PUT' : method, headers, redirect: 'error', signal: AbortSignal.timeout(90_000),
      body: file?.bytes ?? (body ? JSON.stringify(body) : undefined),
    });
    if (!response.ok) {
      await response.body?.cancel();
      throw new ReportBackendError(response.status);
    }
    try { return await response.json() as T; }
    catch { throw new Error('Invalid report backend response'); }
  }

  function scope({ customerID, requestID }: ReportKey) {
    if (typeof customerID !== 'string' || !customerID.trim() || !UUID.test(requestID)) {
      throw new Error('An authenticated customer and a lowercase request UUID are required');
    }
    return [endpointID, customerID, requestID];
  }

  async function find(key: string[]) {
    const { rows } = await db.query(
      `SELECT r.thread_id, r.sha256, r.grant_expires_at, r.published_at, to_jsonb(p) AS regional
         FROM report_requests r LEFT JOIN report_regional_pipelines p ON p.thread_id=r.thread_id
        WHERE r.endpoint_id=$1 AND r.customer_id=$2 AND r.request_id=$3`, key);
    return rows[0] as RequestRow | undefined;
  }

  async function getStagedRequest(key: string[]) {
    const row = await find(key);
    if (!row) throw new Error('Report request disappeared during intake');
    return row;
  }

  async function inboxProgress(row: RequestRow): Promise<Progress> {
    // Wait for the final assistant turn as well as the result materialization.
    const { rows } = await db.query(
      `SELECT EXISTS (SELECT 1 FROM agent_messages WHERE thread_id=$1 AND role='user' AND message_id=$1) AS received,
         (result.content->'report') AS report,
         EXISTS (SELECT 1 FROM agent_messages AS final WHERE final.thread_id=$1
                   AND final.message_id='assistant:' || result.message_id AND final.content->'final'='true'::jsonb) AS finished
         FROM (SELECT 1) AS lookup LEFT JOIN LATERAL
           (SELECT message_id, content FROM agent_messages WHERE thread_id=$1 AND role='tool' ORDER BY seq DESC LIMIT 1) AS result ON true`,
      [row.thread_id]);
    return rows[0] as Progress;
  }

  function toStatus(requestID: string, row: RequestRow, { received, finished, report }: Progress): ReportStatus {
    if (finished && report) {
      if (report.outcome !== 'succeeded') return { requestID, status: 'failed', outcome: report.outcome };
      const { currency, orders, units, revenue_cents } = report.summary;
      return { requestID, status: 'succeeded', summary: { currency, orders, units, revenue_cents } };
    }
    if (received) return { requestID, status: 'running' };
    if (row.grant_expires_at && row.grant_expires_at.getTime() <= Date.now()) return { requestID, status: 'intake_expired' };
    if (row.published_at) return { requestID, status: 'queued' };
    return { requestID, status: 'staging' };
  }

  async function stageGrant(key: string[], row: RequestRow, csv: Blob) {
    const prepared = await request<{ id: string; status: string }>(
      '/v1/execution-files', { body: { size_bytes: csv.size, sha256: row.sha256 } });
    if (!UUID.test(prepared.id)) throw new Error('Invalid Run file identity');
    if (prepared.status !== 'ready') {
      const result = await request<{ status: string }>(`/v1/execution-files/${prepared.id}/content`,
        { file: { bytes: csv, sha256: row.sha256 } });
      if (result.status !== 'ready') throw new Error('Run file upload is pending');
    }
    const callID = `assistant:${row.thread_id}`;
    const grant = await request<{ grant_token: string; expires_at: string }>(`/v1/execution-files/${prepared.id}/agent-grants`, { body: {
      endpoint_id: endpointID, thread_id: row.thread_id, tool_call_id: callID,
      task_name: taskName, version: 1, step_label: 'csv-report', path: 'orders.csv',
    } });
    if (!/^agf1\.[!-~]{1,1531}$/.test(grant.grant_token) || !(Date.parse(grant.expires_at) > Date.now())) {
      throw new Error('Invalid Run file grant');
    }
    // The update arbitrates concurrent submitters. A single statement commits
    // the expiry and its exact token before any caller can publish the inbox row.
    await db.query(
      `WITH staged AS (
         UPDATE report_requests SET grant_expires_at=$6
          WHERE endpoint_id=$1 AND customer_id=$2 AND request_id=$3 AND grant_expires_at IS NULL
          RETURNING endpoint_id, thread_id, request_id
       )
       INSERT INTO report_upload_grants (endpoint_id, thread_id, call_id, upload_ref, grant_token)
       SELECT endpoint_id, thread_id::text, $4, request_id, $5 FROM staged`,
      [...key, callID, grant.grant_token, grant.expires_at]);
  }

  const regional = createRegionalReports({ db, find, request, prefix, deliveries, endpointID,
    backgroundDelivery: options.completionWebhookSecret !== undefined });
  const receiveCompletion = options.completionWebhookSecret === undefined
    ? async () => new Response(null, { status: 404 })
    : createReportCompletionReceiver(options.completionWebhookSecret, regional.complete);

  async function readStatus(requestID: string, row: RequestRow): Promise<ReportStatus> {
    return row.regional ? regional.status(requestID, row.regional) : toStatus(requestID, row, await inboxProgress(row));
  }

  return {
    receiveCompletion,
    async submit(key: ReportKey, csv: Blob): Promise<ReportStatus> {
      const id = scope(key);
      if (!Number.isSafeInteger(csv.size) || csv.size < 1 || csv.size > MAX_FILE_BYTES) throw new Error('Provide a non-empty CSV of up to 64 MiB');
      const hash = createHash('sha256');
      for await (const chunk of csv.stream()) hash.update(chunk);
      const digest = `sha256:${hash.digest('hex')}`;
      await db.query(
        `INSERT INTO report_requests (endpoint_id, customer_id, request_id, thread_id, sha256)
         VALUES ($1, $2, $3, $4, $5) ON CONFLICT (endpoint_id, customer_id, request_id) DO NOTHING`,
        [...id, randomUUID(), digest]);
      let row = await getStagedRequest(id);
      if (row.regional || row.sha256 !== digest) throw new Error('Report request ID already belongs to different CSV bytes');
      if (!row.grant_expires_at) {
        await stageGrant(id, row, csv);
        row = await getStagedRequest(id);
      }
      const current = await readStatus(key.requestID, row);
      if (current.status !== 'staging') return current;
      await request(inbox, { body: {
        idempotency_key: row.thread_id,
        data_base64: Buffer.from(JSON.stringify({ upload_ref: key.requestID })).toString('base64'),
        metadata: { kind: 'user', thread_id: row.thread_id, message_id: row.thread_id },
      } });
      await db.query(
        `UPDATE report_requests SET published_at=COALESCE(published_at, now())
          WHERE endpoint_id=$1 AND customer_id=$2 AND request_id=$3`, id);
      return readStatus(key.requestID, await getStagedRequest(id));
    },
    async submitRegional(key: ReportKey, orders: readonly ReportOrder[]): Promise<ReportStatus> {
      return regional.submit(scope(key), orders);
    },
    async rerun(key: ReportKey, intent: ReportRerun): Promise<ReportStatus | null> {
      const parent = await find(scope(key));
      if (!parent) return null;
      if (!parent.regional) throw new Error('Branch reruns are available for regional reports');
      if (intent.requestID === key.requestID) throw new Error('Generate a new request UUID for the rerun');
      return regional.rerun(parent.regional, scope({ customerID: key.customerID, requestID: intent.requestID }), intent.repeatSteps);
    },
    async status(key: ReportKey): Promise<ReportStatus | null> {
      const row = await find(scope(key));
      return row ? readStatus(key.requestID, row) : null;
    },
    async progress(key: ReportKey, after = 0): Promise<ReportProgress | null> {
      if (!Number.isSafeInteger(after) || after < 0) throw new Error('Invalid report progress cursor');
      const row = await find(scope(key));
      if (!row) return null;
      const pipeline = row.regional ? await regional.progress(row.regional, after) : null;
      return { report: await readStatus(key.requestID, row), pipeline };
    },
    async download(key: ReportKey): Promise<ReportDownload | null> {
      const row = await find(scope(key));
      if (!row) return null;
      let delivery: ReportDelivery | undefined;
      if (row.regional) {
        const status = await regional.status(key.requestID, row.regional);
        if (status.status !== 'succeeded') return null;
        delivery = row.regional.report?.delivery;
      } else {
        const { finished, report } = await inboxProgress(row);
        if (!finished || report?.outcome !== 'succeeded') return null;
        delivery = report.delivery;
      }
      if (!delivery || !UUID.test(delivery.id) || delivery.status !== 'completed' ||
          delivery.receipt_url !== `${deliveries}/${delivery.id}`) throw new Error('Invalid private delivery receipt');
      return request<ReportDownload>(`${deliveries}/${delivery.id}/download-url`);
    },
  };
}
