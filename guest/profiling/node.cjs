// Managed Node profiling bootstrap (ADR-819, ADR-967). Failures affect
// diagnostics only. Dormant collectors poll slowly and load no profiler until
// continuous collection or an on-demand capture enables them.
'use strict';
if (process.env.FAAS_PROFILING_ENABLED === '1') {
  const http = require('node:http');
  let profiler, pprof, epoch = '', running = false, capture = null, heapRunning = false, heapFrom = 0;
  const endpoint = process.env.FAAS_PROFILING_ENDPOINT || 'http://127.0.0.1:9191';
  const request = (path, method = 'GET', body = null) => new Promise((resolve, reject) => {
    const req = http.request(endpoint + path, {method, timeout: body ? 2000 : 200,
      headers: body ? {'Content-Type': 'application/octet-stream', 'Content-Length': body.length} : {}}, res => {
      let data = '';
      res.on('data', chunk => { data += chunk; if (data.length > 4096) req.destroy(); });
      res.on('end', () => {
        if (res.statusCode >= 300) return reject(new Error('profile control unavailable'));
        try { resolve(data ? JSON.parse(data) : {}); } catch (err) { reject(err); }
      });
    });
    req.on('error', reject); req.on('timeout', () => req.destroy());
    if (body) req.write(body);
    req.end();
  });
  const kinds = config => (config.kinds && config.kinds.length ? config.kinds : ['cpu']);
  const upload = (kind, tag, from, body) => {
    const name = 'gregale{gregale_epoch=' + tag + ',gregale_process=' + process.pid + '}';
    const query = '?kind=' + kind + '&name=' + encodeURIComponent(name) +
      '&from=' + Math.floor(from / 1000) + '&until=' + Math.floor(Date.now() / 1000);
    return request('/ingest' + query, 'POST', body).catch(() => {});
  };
  // Heap: V8's sampling heap profiler reports allocations sampled since it
  // started that are still live, so a capture shows memory retained during
  // its window and continuous collection shows the process's live heap.
  async function stopHeap(tag) {
    if (!heapRunning) return;
    try { await upload('heap', tag, heapFrom, await pprof.encode(pprof.heap.profile())); }
    finally { pprof.heap.stop(); heapRunning = false; }
  }
  function startHeap() {
    pprof = pprof || require('@datadog/pprof');
    pprof.heap.start(512 * 1024, 64); heapRunning = true; heapFrom = Date.now();
  }
  // On-demand CPU uses the same time profiler as the continuous Pyroscope
  // collector, stopped explicitly so the window flushes at its end.
  async function stopCapture() {
    const c = capture; capture = null;
    if (!c) return;
    // Read both profiles before encoding so the heap excludes the encoder.
    let cpu = null, heap = null;
    try { if (c.cpu) cpu = pprof.time.stop(); } catch (_) { /* best effort */ }
    try { if (c.heap && heapRunning) heap = pprof.heap.profile(); } catch (_) { /* best effort */ }
    if (heapRunning) { pprof.heap.stop(); heapRunning = false; }
    if (cpu) await upload('cpu', c.epoch, c.from, await pprof.encode(cpu));
    if (heap) await upload('heap', c.epoch, c.from, await pprof.encode(heap));
  }
  function startCapture(config) {
    pprof = pprof || require('@datadog/pprof');
    const want = kinds(config);
    capture = {epoch: config.epoch, from: Date.now(), cpu: want.includes('cpu'), heap: want.includes('heap')};
    if (capture.cpu) {
      // Same options as @pyroscope/nodejs's wall profiler: CPU time
      // collection requires sample contexts.
      pprof.time.start({intervalMicros: 10000, durationMillis: config.window_seconds * 1000,
        withContexts: true, workaroundV8Bug: true, collectCpuTime: true});
      pprof.time.setContext({});
    }
    if (capture.heap) startHeap();
  }
  async function stopContinuous(flush) {
    if (running) { await profiler.stopWallProfiling(); running = false; }
    if (heapRunning) {
      if (flush) await stopHeap(epoch);
      else { pprof.heap.stop(); heapRunning = false; }
    }
  }
  async function tick() {
    let active = false;
    try {
      const config = await request('/control?pid=' + process.pid);
      if (capture && (config.suspended || config.epoch !== capture.epoch || !config.enabled)) {
        // A new epoch means a checkpoint or restore: discard the window.
        if (config.epoch === capture.epoch) await stopCapture();
        else { capture = null; try { pprof.time.stop(); } catch (_) {} if (heapRunning) { pprof.heap.stop(); heapRunning = false; } }
      }
      if ((running || heapRunning) && !capture && (config.suspended || config.epoch !== epoch || !config.enabled || config.capture)) {
        await stopContinuous(!config.suspended && config.epoch === epoch);
      }
      if (config.suspended) {
        await request('/control/ack?pid=' + process.pid + '&epoch=' + config.epoch, 'POST');
      } else if (config.enabled && config.capture && !capture && config.epoch !== epoch) {
        epoch = config.epoch;
        startCapture(config);
      } else if (config.enabled && !config.capture && !running && !heapRunning) {
        const want = kinds(config);
        epoch = config.epoch;
        if (want.includes('cpu')) {
          profiler = profiler || require('@pyroscope/nodejs').default;
          profiler.init({serverAddress:endpoint, appName:'gregale',
            flushIntervalMs:config.window_seconds * 1000,
            wall:{samplingDurationMs:config.window_seconds * 1000, collectCpuTime:true, samplingIntervalMicros:10000},
            tags:{gregale_epoch:config.epoch, gregale_process:String(process.pid)}});
          profiler.startWallProfiling(); running = true;
        }
        if (want.includes('heap')) startHeap();
      } else if (heapRunning && !capture && Date.now() - heapFrom >= config.window_seconds * 1000) {
        // Continuous heap: report the live heap once per window.
        const from = heapFrom; heapFrom = Date.now();
        await upload('heap', epoch, from, await pprof.encode(pprof.heap.profile()));
      }
      active = config.enabled || running || heapRunning || capture !== null;
    } catch (_) { /* diagnostics never prevent the application from serving */ }
    setTimeout(tick, active ? 100 : 1000).unref();
  }
  tick();
}
