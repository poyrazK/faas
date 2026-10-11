// A stable report identity lets approved workflow resumes reuse retained bytes.
export async function finishExport(runtime, headers, result, scope) {
  const finish = async active => {
    await active.checkpoint();
    const context = runtime.context();
    if (!context || context.step !== 'finish') throw new Error('Trusted final workflow action required');
    const artifact = await runtime.uploadArtifact({report_id: 'export-csv', name: 'export.csv', data: result.csv, maxBytes: 4096});
    active.throwIfStopped();
    return {rows: result.rows, artifact_id: artifact.id};
  };
  return scope ? finish(scope) : runtime.runCancellableRequest(headers, finish);
}
