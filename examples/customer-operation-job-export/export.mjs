import {setImmediate as yieldWork} from 'node:timers/promises';
import {MAX_EXPORT_BYTES} from './contract.mjs';

export async function generateExport(input, scope) {
  if (!input || Object.keys(input).join(',') !== 'count' || !Number.isInteger(input.count) || input.count < 1 || input.count > 1000) throw new Error('Invalid export input');
  await scope.progress({report_id: 'generating', stage: 'generating', completed: 0, total: input.count});
  const lines = ['id,value\n'];
  for (let i = 1; i <= input.count; i++) {
    scope.throwIfStopped(); lines.push(`${i},${i * 10}\n`);
    if (i % 100 === 0) { await yieldWork(undefined, {signal: scope.signal}); await scope.checkpoint(); }
  }
  await scope.checkpoint();
  const artifact = await scope.uploadArtifact({report_id: 'export-csv', name: 'export.csv', data: lines.join(''), maxBytes: MAX_EXPORT_BYTES});
  scope.throwIfStopped();
  // Returning prepares the typed result. Only a successful native task exit
  // publishes it and its private file; completion delivery has its own state.
  return {rows: input.count, artifact_id: artifact.id};
}
