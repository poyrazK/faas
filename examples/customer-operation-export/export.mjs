// ADR-521: an ordinary HTTP handler reports progress through its trusted claim.
export async function generateExport(runtime, headers, input) {
  if (!input || Object.keys(input).join(',') !== 'count' || !Number.isInteger(input.count) || input.count < 1 || input.count > 1000) throw new Error('Invalid export input');
  return runtime.runRequest(headers, async () => {
    if (!runtime.context()) throw new Error('Trusted operation execution required');
    await runtime.progress({report_id: 'generating', stage: 'generating', completed: 0, total: input.count});
    const csv = 'id,value\n' + Array.from({length: input.count}, (_, i) => `${i+1},${(i+1)*10}\n`).join('');
    await runtime.progress({report_id: 'complete', stage: 'complete', completed: input.count, total: input.count});
    return {csv, rows: input.count};
  });
}
