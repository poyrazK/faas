/** Portable payload contracts for authenticated application CloudEvents. */
import { JobContract, JobError, jobSnapshot, jobValueHash, type JobSchema } from './job-runtime.js';

export interface EventContractSpec {
  readonly version: 1;
  readonly type: string;
  readonly data: JobSchema;
}
/** @internal Match the native event type's bounded UTF-8 string contract. */
export function eventContractType(value: unknown): value is string {
  return typeof value === 'string' && [...value].length >= 1 && [...value].length <= 256
    && Buffer.from(value).toString('utf8') === value && !/[\r\n\0]/.test(value);
}
export class EventContract<T> {
  readonly spec: EventContractSpec;
  readonly sha256: string;
  private readonly validator: JobContract<T, null>;
  constructor(spec: EventContractSpec) {
    if (!spec || Object.keys(spec).sort().join() !== 'data,type,version' || spec.version !== 1
      || !eventContractType(spec.type)) throw new JobError('invalid_contract');
    this.validator = new JobContract<T, null>({ version: 1, name: 'event-payload', input: spec.data, output: { type: 'null' } });
    this.spec = jobSnapshot(spec);
    this.sha256 = jobValueHash(this.spec);
    Object.freeze(this);
  }
  input(value: unknown): T { return this.validator.input(value); }
}
