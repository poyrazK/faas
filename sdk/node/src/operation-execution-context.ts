export interface OperationExecutionContext { id: string; attempt: number; capability: string; invocationID: string }
export type OperationRequestHeaders = HeadersInit | Record<string, string | readonly string[] | undefined>;

export function operationHeaders(headers: OperationRequestHeaders): Headers {
  return headers instanceof Headers || Array.isArray(headers) ? new Headers(headers) : new Headers(
    Object.entries(headers).filter((entry): entry is [string, string | readonly string[]] => entry[1] !== undefined)
      .map(([key, value]): [string, string] => [key, typeof value === 'string' ? value : value.join(', ')]),
  );
}

export function operationExecutionContext(headers: Headers, id: string): OperationExecutionContext {
  const invocationID = headers.get('X-Faas-Invocation-Id') ?? '';
  const attemptRaw = headers.get('X-Gregale-Operation-Attempt') ?? '';
  const capability = headers.get('X-Gregale-Operation-Capability') ?? '';
  const uuid = (value: string): boolean => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000';
  const positive = (value: string): boolean => /^[1-9][0-9]*$/.test(value) && Number.isSafeInteger(Number(value));
  const invalid = (): never => { throw new Error('Invalid operation execution context'); };
  if (!uuid(id) || !positive(attemptRaw) || !/^[0-9a-f]{64}$/.test(capability) || !uuid(invocationID)) return invalid();
  headers.forEach((_value, name) => {
    if (name.startsWith('x-gregale-operation-') && !['x-gregale-operation-attempt', 'x-gregale-operation-capability'].includes(name)) invalid();
    if (name.startsWith('x-gregale-customer-operation-') && ![
      'x-gregale-customer-operation-id', 'x-gregale-customer-operation-transaction-version', 'x-gregale-customer-operation-result-max-bytes', 'x-gregale-customer-operation-milestone-version',
    ].includes(name)) invalid();
  });
  return { id, attempt: Number(attemptRaw), capability, invocationID };
}
