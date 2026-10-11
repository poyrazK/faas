// ADR-521: an ordinary HTTP handler reports progress through its trusted claim.
import {setImmediate as yieldWork} from 'node:timers/promises';

export async function generateExport(runtime, headers, input) {
  if (!input || Object.keys(input).join(',') !== 'count' || !Number.isInteger(input.count) || input.count < 1 || input.count > 1000) throw new Error('Invalid export input');
  return runtime.runCancellableRequest(headers, async scope => {
    if (!runtime.context()) throw new Error('Trusted operation execution required');
    await runtime.progress({report_id: 'generating', stage: 'generating', completed: 0, total: input.count});
    scope.throwIfStopped();
    const rows = ['id,value\n'];
    for (let i = 1; i <= input.count; i++) {
      rows.push(`${i},${i*10}\n`);
      if (i % 100 === 0) { await yieldWork(undefined, {signal: scope.signal}); scope.throwIfStopped(); }
    }
    const csv = rows.join('');
    const artifact = await runtime.uploadArtifact({report_id: 'export-file', name: 'export.csv', data: csv, maxBytes: 32768});
    return {artifact_id: artifact.id, rows: input.count};
  });
}
