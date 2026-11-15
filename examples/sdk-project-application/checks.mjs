/** Application credentials belong here, separately from the control-plane SDK. */
export function applicationChecks(target, applicationURL) {
  const url = new URL(applicationURL);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) throw new TypeError('Invalid application URL');
  const worker = target.workloads.find(w => w.slug.endsWith('-worker'));
  if (!worker?.deployment_id) throw new TypeError('An explicit worker deployment is required');
  url.searchParams.set('worker_deployment_id', worker.deployment_id);
  return target.workloads.map(workload => ({name:workload.slug, workload:workload.slug, async run(context) {
    const response = await fetch(url, {signal:context.signal,redirect:'error',headers:{Accept:'application/json'}});
    const reader = response.body?.getReader();
    if (!reader) return {outcome:'inconclusive'};
    let size = 0; const chunks = [];
    try {for (;;) {const {done,value} = await reader.read();if (done) break;size += value.byteLength;if (size > 16384) return {outcome:'inconclusive'};chunks.push(value);}}
    finally {await reader.cancel();}
    if (!response.ok) return {outcome:'inconclusive'};
    const value = JSON.parse(Buffer.concat(chunks).toString());
    if (!workload.slug.endsWith('-worker')) return {outcome:value.ok === true && value.deployment_id === context.deployment_id ? 'verified' : 'inconclusive',deployment_id:value.deployment_id};
    const observed = value.worker, age = Date.now() - Date.parse(observed?.observed_at);
    if (!observed || observed.deployment_id !== context.deployment_id || !Number.isFinite(age) || age < 0 || age > 15000) return {outcome:'inconclusive'};
    return {outcome:observed.status === 'failed' ? 'failed' : observed.status === 'processed' && value.processed > 0 ? 'verified' : 'inconclusive',deployment_id:observed.deployment_id};
  }}));
}
