// ADR-719/647: app-owned workflow state snapshots with transactional continuity.
import { randomUUID } from 'node:crypto';
import { OPERATION_WORKFLOW_STATE_BATCH_BYTES, OPERATION_WORKFLOW_STATE_REPORTS } from './operation-contract.js';
import type { CustomerOperationTransactionRequest } from './customer-operation-transactions.js';
import { customerOperationRequestDigest } from './customer-operation-transactions.js';
import type { OperationPool, OperationTransaction } from './operation-receipt.js';
import type { OperationMilestoneReport } from './customer-operations.js';

export interface OperationWorkflowBlockerResolution { code: string; operation: string; description: string; blocker_operation_id: string; blocker_report_id: string; blocker_revision: number }

export interface OperationWorkflowBlocker { first_observed_at?: string; code: string; description: string; operation: string }

export interface OperationWorkflowEvidenceMilestone { id: string; name: string }

export interface OperationWorkflowDependency { subject_type: string; subject_id: string; workflow: string; instance_id: string; required_outcome_code?: string }
export interface OperationWorkflowStateReport {
 depends_on?: OperationWorkflowDependency[]; dependencies_only?: boolean;
 outcome_code?: string; outcome_description?: string; outcome_only?: boolean;
 deadline_at?: string;
 deadline_only?: boolean;
  blocker_resolutions?: OperationWorkflowBlockerResolution[];
  blockers?: OperationWorkflowBlocker[];
  blockers_only?: boolean;
  id: string;
  workflow: string;
  instance_id: string;
  from_state?: string;
  state: string;
  revision: number;
  occurred_at: string;
  evidence_milestones?: OperationWorkflowEvidenceMilestone[];
}

export interface OperationWorkflowStateReceipt extends Omit<OperationWorkflowStateReport, 'occurred_at' | 'evidence_milestones'> {
  operation_id: string;
  contract_version: number;
  evidence_milestones?: OperationWorkflowEvidenceMilestone[];
}

const WORKFLOW = /^[a-z][a-z0-9-]{0,62}$/;
const STATE = /^[a-z][a-z0-9-]{0,63}$/;

export function workflowStateTransaction(
  request: CustomerOperationTransactionRequest,
  reports: OperationWorkflowStateReport[],
  isOpen: () => boolean,
): {
  workflowDependencies(workflow: string, instanceID: string, state: string, dependencies: OperationWorkflowDependency[]): void;
  workflowOutcome(workflow: string, instanceID: string, state: string, code: string, description: string): void;
  workflowDeadline(workflow: string, instanceID: string, state: string, dueAt: string): void;
  workflowBlockers(workflow: string, instanceID: string, state: string, blockers: OperationWorkflowBlocker[], resolutions?: OperationWorkflowBlockerResolution[]): void;
  workflowState(workflow: string, instanceID: string, state: string): void;
  workflowTransition(workflow: string, instanceID: string, fromState: string, toState: string): void;
} {
  const queue = (workflow: string, instanceID: string, state: string, fromState?: string, blockers?: OperationWorkflowBlocker[], blockersOnly = false, resolutions?: OperationWorkflowBlockerResolution[], deadline?: string, outcome?: {code: string; description: string}, dependencies?: OperationWorkflowDependency[]) => {
    if (!isOpen()) throw new TypeError('Workflow state must be reported inside the transaction callback');
    if (!request.milestonesSupported) throw new TypeError('Customer Operation workflow states are not negotiated');
    if (typeof workflow !== 'string' || !WORKFLOW.test(workflow) || typeof state !== 'string' || !STATE.test(state)
        || fromState !== undefined && (typeof fromState !== 'string' || !STATE.test(fromState))
        || typeof instanceID !== 'string' || !instanceID || Buffer.byteLength(instanceID) > 256
        || /[\x00-\x1f\x7f]/.test(instanceID) || /[\uD800-\uDFFF]/u.test(instanceID)) {
      throw new TypeError('Workflow state requires valid workflow, instance ID, and state names');
    }
    if (reports.length >= OPERATION_WORKFLOW_STATE_REPORTS) throw new TypeError('Too many workflow state updates');
    const report: OperationWorkflowStateReport = {
      id: randomUUID(), workflow, instance_id: instanceID, state, revision: 0, occurred_at: new Date().toISOString(),
    };
    if (dependencies !== undefined) {report.depends_on=canonicalWorkflowDependencies(dependencies);report.dependencies_only=true;}
    if (outcome !== undefined) {validateWorkflowOutcome(outcome.code,outcome.description);report.outcome_code=outcome.code;report.outcome_description=outcome.description;report.outcome_only=true;}
    if (deadline !== undefined) {report.deadline_at=canonicalWorkflowDeadline(deadline);report.deadline_only=true;}
    if (fromState !== undefined) report.from_state = fromState;
    if (blockers !== undefined) report.blockers = canonicalWorkflowBlockers(blockers);
    if (blockersOnly) report.blockers_only = true;
    if (resolutions !== undefined) report.blocker_resolutions = canonicalWorkflowResolutions(resolutions, report.blockers ?? []);
    if (Buffer.byteLength(JSON.stringify({workflow_states: [...reports, report]})) > OPERATION_WORKFLOW_STATE_BATCH_BYTES) throw new TypeError('Workflow state batch exceeds its byte limit');
    reports.push(report);
  };
  return {
    workflowDependencies(workflow,instanceID,state,dependencies) {queue(workflow,instanceID,state,state,undefined,false,undefined,undefined,undefined,dependencies);},
    workflowOutcome(workflow,instanceID,state,code,description) {queue(workflow,instanceID,state,state,undefined,false,undefined,undefined,{code,description});},
    workflowDeadline(workflow,instanceID,state,dueAt) { queue(workflow,instanceID,state,state,undefined,false,undefined,dueAt); },
    workflowBlockers(workflow, instanceID, state, blockers, resolutions) { queue(workflow, instanceID, state, state, blockers, true, resolutions); },
    workflowState(workflow, instanceID, state) { queue(workflow, instanceID, state); },
    workflowTransition(workflow, instanceID, fromState, toState) { queue(workflow, instanceID, toState, fromState); },
  };
}

export async function saveCustomerWorkflowStates(
  tx: OperationTransaction,
  request: CustomerOperationTransactionRequest,
  reports: OperationWorkflowStateReport[],
  milestones: OperationMilestoneReport[],
): Promise<OperationWorkflowStateReport[]> {
  const saved: OperationWorkflowStateReport[] = [];
  for (const report of reports) {
    const counter = (await tx.query(
      `INSERT INTO public.gregale_customer_operation_workflow_state_counters(platform_tenant_id,workflow,instance_id,revision)
       VALUES ($1::uuid,$2,$3,1)
       ON CONFLICT (platform_tenant_id,workflow,instance_id) DO UPDATE
        SET revision=public.gregale_customer_operation_workflow_state_counters.revision+1
        WHERE public.gregale_customer_operation_workflow_state_counters.revision<9007199254740991
       RETURNING revision`, [request.platformTenantId, report.workflow, report.instance_id],
    )).rows[0];
    const revision = Number(counter?.revision);
    if (!Number.isSafeInteger(revision) || revision < 1) throw new TypeError('Workflow state revision is unavailable');
    const value = {...report, revision};
    if (!value.blockers_only && !value.deadline_only && !value.outcome_only && !value.dependencies_only && value.from_state !== undefined && milestones.length > 0) {
      const byName = new Map<string, OperationWorkflowEvidenceMilestone>();
      for (const milestone of milestones) if (!byName.has(milestone.name)) byName.set(milestone.name, {id: milestone.id, name: milestone.name});
      const evidence = [...byName.values()].sort((left, right) => left.name.localeCompare(right.name) || left.id.localeCompare(right.id));
      if (evidence.length > 16) throw new TypeError('Workflow transition evidence exceeds its limit');
      if (evidence.length > 0) value.evidence_milestones = evidence;
    }
    // The counter upsert above serializes writers for this run until commit.
    // Its head survives receipt cleanup, so the check does not depend on old
    // outbox rows remaining in the application database.
    const head = (await tx.query(
      `SELECT last_state,last_blockers,last_blockers_revision,last_deadline_at,last_deadline_revision,last_outcome_code,last_outcome_description,last_outcome_revision,last_dependencies,last_dependencies_revision FROM public.gregale_customer_operation_workflow_state_counters
       WHERE platform_tenant_id=$1::uuid AND workflow=$2 AND instance_id=$3 FOR UPDATE`,
      [request.platformTenantId, value.workflow, value.instance_id],
    )).rows[0];
    if (head === undefined) throw new TypeError('Workflow state counter is unavailable');
    const priorState = head.last_state;
    if (priorState !== null && priorState !== undefined && (typeof priorState !== 'string' || !STATE.test(priorState))) {
      throw new TypeError('Saved workflow state is invalid');
    }
    if (value.from_state !== undefined && typeof priorState === 'string' && priorState !== value.from_state) {
      throw new TypeError('Workflow transition source does not match the latest app-reported state');
    }
    const previous = canonicalWorkflowBlockers(typeof head.last_blockers === 'string' ? JSON.parse(head.last_blockers) : head.last_blockers);
    if (value.deadline_only || value.outcome_only || value.dependencies_only) {
      if (Number(head.last_blockers_revision)!==revision-1) throw new TypeError('Metadata update requires current blocker metadata; upgrade all writers');
      value.blockers=previous;
    }
    if (!value.deadline_only && !value.deadline_at && Number(head.last_deadline_revision)===revision-1) value.deadline_at=canonicalWorkflowDeadline(head.last_deadline_at);
    if (!value.outcome_only && !value.outcome_code && priorState===value.state && Number(head.last_outcome_revision)===revision-1) {value.outcome_code=head.last_outcome_code as string;value.outcome_description=head.last_outcome_description as string;}
    if (!value.dependencies_only && value.depends_on===undefined && Number(head.last_dependencies_revision)===revision-1) value.depends_on=canonicalWorkflowDependencies(typeof head.last_dependencies==='string' ? JSON.parse(head.last_dependencies) : head.last_dependencies);
    value.depends_on=canonicalWorkflowDependencies(value.depends_on ?? []);
    if (value.outcome_code || value.outcome_description) validateWorkflowOutcome(value.outcome_code,value.outcome_description);
    if (value.deadline_at!==undefined) value.deadline_at=canonicalWorkflowDeadline(value.deadline_at);
    value.blockers = canonicalWorkflowBlockers(value.blockers ?? []).map(b => {
      if (Number(head.last_blockers_revision) === revision - 1) {
        const prior = previous.find(p => p.code === b.code && p.operation === b.operation);
        if (prior?.first_observed_at) b.first_observed_at = prior.first_observed_at;
        else if (!prior && !b.first_observed_at) b.first_observed_at = value.occurred_at;
      }
      if (b.first_observed_at && new Date(b.first_observed_at).getTime() > new Date(value.occurred_at).getTime()) throw new TypeError('Blocker first observation exceeds report time');
      return b;
    });
    await tx.query(
      `INSERT INTO public.gregale_customer_operation_workflow_states(operation_id,id,platform_tenant_id,workflow,instance_id,from_state,state,revision,evidence_milestones,occurred_at,blockers,blockers_only,blocker_resolutions,deadline_at,deadline_only,outcome_code,outcome_description,outcome_only,depends_on,dependencies_only)
       VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9::jsonb,$10::timestamptz,$11::jsonb,$12::boolean,$13::jsonb,$14::text,$15::boolean,$16::text,$17::text,$18::boolean,$19::jsonb,$20::boolean)`,
      [request.operationId, value.id, request.platformTenantId, value.workflow, value.instance_id, value.from_state ?? '', value.state, value.revision, JSON.stringify(value.evidence_milestones ?? []), value.occurred_at, JSON.stringify(value.blockers ?? []), value.blockers_only ?? false, JSON.stringify(value.blocker_resolutions ?? []),value.deadline_at ?? '',value.deadline_only ?? false,value.outcome_code ?? '',value.outcome_description ?? '',value.outcome_only ?? false,JSON.stringify(value.depends_on),value.dependencies_only ?? false],
    );
    const updatedHead = (await tx.query(
      `UPDATE public.gregale_customer_operation_workflow_state_counters SET last_state=$4,last_blockers=$6::jsonb,last_blockers_revision=$5,last_deadline_at=$7,last_deadline_revision=$5,last_outcome_code=$8,last_outcome_description=$9,last_outcome_revision=$5,last_dependencies=$10::jsonb,last_dependencies_revision=$5
       WHERE platform_tenant_id=$1::uuid AND workflow=$2 AND instance_id=$3 AND revision=$5
       RETURNING revision`,
      [request.platformTenantId, value.workflow, value.instance_id, value.state, value.revision, JSON.stringify(value.blockers ?? []),value.deadline_at ?? '',value.outcome_code ?? '',value.outcome_description ?? '',JSON.stringify(value.depends_on)],
    )).rows[0];
    if (Number(updatedHead?.revision) !== value.revision) throw new TypeError('Workflow state counter changed unexpectedly');
    saved.push(value);
  }
  return saved;
}

export class OperationWorkflowStatePublicationError extends Error {
  readonly code = 'operation_workflow_state_publication_incomplete';
  readonly committed = true;
  constructor(cause: unknown) {
    super('Business transaction committed; workflow state publication incomplete. Retry the same operation.', {cause});
    this.name = 'OperationWorkflowStatePublicationError';
  }
}

export async function publishCustomerWorkflowStates(
  pool: OperationPool,
  request: CustomerOperationTransactionRequest,
  publish: (report: OperationWorkflowStateReport) => Promise<OperationWorkflowStateReceipt>,
): Promise<void> {
  if (!request.milestonesSupported) return;
  let connection;
  try {
    connection = await pool.connect();
    const digest = Buffer.from(customerOperationRequestDigest(request));
    const values = [request.operationId, request.accountId, request.appId, request.platformTenantId, digest];
    const rows = (await connection.query(
      `SELECT s.id::text,s.workflow,s.instance_id,s.from_state,s.state,s.revision,s.evidence_milestones,s.occurred_at,s.blockers,s.blockers_only,s.blocker_resolutions,s.deadline_at,s.deadline_only,s.outcome_code,s.outcome_description,s.outcome_only,s.depends_on,s.dependencies_only FROM public.gregale_customer_operation_workflow_states s
       JOIN public.gregale_customer_operation_inbox r ON r.operation_id=s.operation_id
       WHERE r.operation_id=$1::uuid AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid
        AND r.request_digest=$5 AND s.acknowledged_at IS NULL ORDER BY s.revision,s.id LIMIT $6`, [...values, OPERATION_WORKFLOW_STATE_REPORTS + 1],
    )).rows;
    if (rows.length > OPERATION_WORKFLOW_STATE_REPORTS) throw new TypeError('Saved workflow state count exceeds its bound');
    for (const row of rows) {
      const revision = Number(row.revision);
      if (typeof row.id !== 'string' || typeof row.workflow !== 'string' || typeof row.instance_id !== 'string'
          || typeof row.state !== 'string' || typeof row.from_state !== 'string' || !Number.isSafeInteger(revision) || revision < 1) throw new TypeError('Invalid saved workflow state');
      const date = row.occurred_at instanceof Date ? row.occurred_at : new Date(String(row.occurred_at));
      if (!Number.isFinite(date.valueOf())) throw new TypeError('Invalid saved workflow state timestamp');
      const report: OperationWorkflowStateReport = {id: row.id, workflow: row.workflow, instance_id: row.instance_id, state: row.state, revision, occurred_at: date.toISOString()};
      report.depends_on=canonicalWorkflowDependencies(typeof row.depends_on==='string' ? JSON.parse(row.depends_on) : row.depends_on);
      if (row.dependencies_only===true) report.dependencies_only=true;
      if (row.outcome_code || row.outcome_description) {validateWorkflowOutcome(row.outcome_code,row.outcome_description);report.outcome_code=row.outcome_code as string;report.outcome_description=row.outcome_description as string;}
      if (row.outcome_only===true) report.outcome_only=true;
      if (row.deadline_at) report.deadline_at=canonicalWorkflowDeadline(row.deadline_at);
      if (row.deadline_only===true) report.deadline_only=true;
      if (row.from_state) report.from_state = row.from_state;
      report.blockers = canonicalWorkflowBlockers(typeof row.blockers === 'string' ? JSON.parse(row.blockers) : row.blockers);
      if (row.blockers_only === true) report.blockers_only = true;
      report.blocker_resolutions = canonicalWorkflowResolutions(typeof row.blocker_resolutions === 'string' ? JSON.parse(row.blocker_resolutions) : row.blocker_resolutions, report.blockers);
      const evidence = typeof row.evidence_milestones === 'string' ? JSON.parse(row.evidence_milestones) as unknown : row.evidence_milestones;
      if (!Array.isArray(evidence) || evidence.length > 16 || evidence.some(item => item === null || typeof item !== 'object'
          || typeof (item as Record<string, unknown>).id !== 'string' || typeof (item as Record<string, unknown>).name !== 'string')) throw new TypeError('Invalid saved workflow state evidence');
      if (evidence.length > 0) report.evidence_milestones = evidence as OperationWorkflowEvidenceMilestone[];
      const receipt = await publish(report);
      if (JSON.stringify(canonicalWorkflowDependencies(receipt?.depends_on ?? []))!==JSON.stringify(report.depends_on) || (receipt?.dependencies_only ?? false)!==(report.dependencies_only ?? false) || (receipt?.outcome_code ?? '')!==(report.outcome_code ?? '') || (receipt?.outcome_description ?? '')!==(report.outcome_description ?? '') || (receipt?.outcome_only ?? false)!==(report.outcome_only ?? false) || canonicalWorkflowDeadline(receipt?.deadline_at ?? '')!==canonicalWorkflowDeadline(report.deadline_at ?? '') || (receipt?.deadline_only ?? false)!==(report.deadline_only ?? false) || receipt?.id !== report.id || receipt.operation_id !== request.operationId || receipt.revision !== revision
          || !Number.isSafeInteger(receipt.contract_version) || receipt.contract_version < 1
          || receipt.workflow !== report.workflow || receipt.instance_id !== report.instance_id || receipt.from_state !== report.from_state || receipt.state !== report.state
          || JSON.stringify(receipt.evidence_milestones ?? []) !== JSON.stringify(report.evidence_milestones ?? [])
          || JSON.stringify(canonicalWorkflowBlockers(receipt.blockers ?? [])) !== JSON.stringify(report.blockers ?? []) || (receipt.blockers_only ?? false) !== (report.blockers_only ?? false)
          || JSON.stringify(canonicalWorkflowResolutions(receipt.blocker_resolutions ?? [], receipt.blockers ?? [])) !== JSON.stringify(report.blocker_resolutions)) {
        throw new TypeError('Workflow state publication identity was not confirmed');
      }
      await connection.query(
        `UPDATE public.gregale_customer_operation_workflow_states s SET acknowledged_at=clock_timestamp()
         FROM public.gregale_customer_operation_inbox r WHERE s.operation_id=r.operation_id AND r.operation_id=$1::uuid
          AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid AND r.request_digest=$5 AND s.id=$6::uuid`, [...values, row.id],
      );
    }
  } catch (error) { throw new OperationWorkflowStatePublicationError(error); }
  finally { connection?.release(); }
}

export function canonicalWorkflowBlockers(value: unknown): OperationWorkflowBlocker[] {
 if (!Array.isArray(value) || value.length > 16) throw new TypeError('Workflow blockers must be an array of at most 16 public reasons');
 const seen = new Set<string>();
 const result = value.map((item: unknown) => {
  if (!item || typeof item !== 'object') throw new TypeError('Invalid workflow blocker');
  const {code, description, operation, first_observed_at} = item as Record<string, unknown>;
  if (typeof code !== 'string' || !STATE.test(code) || typeof operation !== 'string' || !STATE.test(operation)
      || typeof description !== 'string' || !description || Buffer.byteLength(description) > 512 || /[\uD800-\uDFFF]/u.test(description) || /[\x00-\x1f\x7f]/.test(description)) throw new TypeError('Invalid workflow blocker public fields');
  const key = operation + ':' + code;
  if (seen.has(key)) throw new TypeError('Duplicate workflow blocker target/code');
  seen.add(key);
  if (first_observed_at !== undefined && (typeof first_observed_at !== 'string' || !/^\d{4}-\d\d-\d\dT.*(?:Z|[+-]\d\d:\d\d)$/.test(first_observed_at) || !Number.isFinite(Date.parse(first_observed_at)) || new Date(first_observed_at).getUTCFullYear()<1)) throw new TypeError('Invalid blocker observation time');
  return first_observed_at === undefined ? {code,description,operation} : {code,description,operation,first_observed_at: canonicalBlockerTimestamp(first_observed_at as string)};
 });
 return result.sort((a,b) => a.operation < b.operation ? -1 : a.operation > b.operation ? 1 : a.code < b.code ? -1 : a.code > b.code ? 1 : 0);
}

export function canonicalWorkflowResolutions(value: unknown, blockers: OperationWorkflowBlocker[]): OperationWorkflowBlockerResolution[] {
 if (!Array.isArray(value) || value.length > 16) throw new TypeError('Workflow resolutions require at most 16 public facts');
 const active = new Set(blockers.map(b => b.operation + ':' + b.code));
 const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
 const nil = '00000000-0000-0000-0000-000000000000';
 const result = value.map((item: unknown) => {
  if (!item || typeof item !== 'object') throw new TypeError('Invalid workflow resolution');
  const v = item as Record<string, unknown>;
  if (typeof v.blocker_operation_id !== 'string' || !uuid.test(v.blocker_operation_id) || v.blocker_operation_id === nil
    || typeof v.blocker_report_id !== 'string' || !uuid.test(v.blocker_report_id) || v.blocker_report_id === nil
    || typeof v.blocker_revision !== 'number' || !Number.isSafeInteger(v.blocker_revision) || v.blocker_revision < 1
    || active.has(String(v.operation) + ':' + String(v.code))) throw new TypeError('Resolution requires a prior report identity/revision and cleared target/code');
  return {code: v.code as string, operation: v.operation as string, description: v.description as string,
   blocker_operation_id: v.blocker_operation_id, blocker_report_id: v.blocker_report_id, blocker_revision: v.blocker_revision};
 });
 canonicalWorkflowBlockers(result);
 return result.sort((a,b) => a.operation < b.operation ? -1 : a.operation > b.operation ? 1 : a.code < b.code ? -1 : a.code > b.code ? 1 : 0);
}

// Preserve microseconds when reports are shared with Go/Python writers.
function canonicalBlockerTimestamp(value: string): string {
 const match = /^(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)$/.exec(value);
 if (!match) throw new TypeError('Invalid blocker observation time');
 const year=Number(match[1]), month=Number(match[2]), day=Number(match[3]);
 const leap=year%4===0 && (year%100!==0 || year%400===0);
 const days=[31,leap?29:28,31,30,31,30,31,31,30,31,30,31];
 if (year<1 || month<1 || month>12 || day<1 || day>days[month-1]! || Number(match[4])>23 || Number(match[5])>59 || Number(match[6])>59) throw new TypeError('Invalid blocker observation time');
 const iso=new Date(value).toISOString();
 if (iso.length!==24 || Number(iso.slice(0,4))<1) throw new TypeError('Invalid blocker observation time');
 const fraction=(match[7] ?? '').slice(0,6).replace(/0+$/,'');
 return iso.slice(0,19)+(fraction ? '.'+fraction : '')+'Z';
}

export function canonicalWorkflowDeadline(value: unknown): string {
 if (value==='') return '';
 if (typeof value!=='string' || !Number.isFinite(Date.parse(value))) throw new TypeError('Deadline requires a finite RFC3339 timestamp');
 return canonicalBlockerTimestamp(value);
}

function validateWorkflowOutcome(code: unknown,description: unknown): void {
 if (typeof code!=='string' || !STATE.test(code) || typeof description!=='string' || !description || Buffer.byteLength(description)>512 || /[\uD800-\uDFFF]/u.test(description) || /[\x00-\x1f\x7f]/.test(description)) throw new TypeError('Outcome requires a bounded public code and description');
}

export function canonicalWorkflowDependencies(value: unknown): OperationWorkflowDependency[] {
 if (!Array.isArray(value) || value.length>16) throw new TypeError('At most 16 direct dependencies are supported');
 const seen=new Set<string>();const validID=(id: unknown): id is string => typeof id==='string' && id.length>0 && Buffer.byteLength(id)<=256 && !/[\uD800-\uDFFF]/u.test(id) && !/[\x00-\x1f\x7f]/.test(id);
 const result=value.map((item: unknown)=>{
  if (!item || typeof item!=='object') throw new TypeError('Invalid dependency');
  const d=item as Record<string,unknown>;
  if (typeof d.subject_type!=='string' || !STATE.test(d.subject_type) || !validID(d.subject_id) || typeof d.workflow!=='string' || !WORKFLOW.test(d.workflow) || !validID(d.instance_id) || d.required_outcome_code!==undefined && (typeof d.required_outcome_code!=='string' || d.required_outcome_code!=='' && !STATE.test(d.required_outcome_code))) throw new TypeError('Invalid dependency reference');
  const key=JSON.stringify([d.subject_type,d.subject_id,d.workflow,d.instance_id]);if(seen.has(key))throw new TypeError('Duplicate dependency');seen.add(key);
  const dep: OperationWorkflowDependency={subject_type:d.subject_type,subject_id:d.subject_id,workflow:d.workflow,instance_id:d.instance_id};if(d.required_outcome_code)dep.required_outcome_code=d.required_outcome_code as string;return dep;
 });
 return result.sort((a,b)=>{for(const key of ['subject_type','subject_id','workflow','instance_id'] as const){if(a[key]<b[key])return -1;if(a[key]>b[key])return 1;}return 0;});
}
