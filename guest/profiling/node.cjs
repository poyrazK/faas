// Managed Node profiling bootstrap (ADR-819, ADR-1155). Failures affect
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
  const upload = (kind, tag, from, body, checkpoint = false) => {
    const name = 'gregale{gregale_epoch=' + tag + ',gregale_process=' + process.pid + '}';
    const query = '?kind=' + kind + '&name=' + encodeURIComponent(name) +
      '&from=' + Math.floor(from / 1000) + '&until=' + Math.floor(Date.now() / 1000) + (checkpoint ? '&checkpoint=1' : '');
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
  // Stacks: a JS thread cannot sample its own stack while it runs this
  // collector, so Node reports what the event loop is waiting on: active
  // handles and requests by type (sockets, timers, file system requests).
  // When the main thread is stuck in JavaScript, guest-init pauses it through
  // the inspector instead (ADR-1155) and marks the epoch here, so this
  // collector does not report the same snapshot again once the thread frees.
  const activeResources = () => (typeof process.getActiveResourcesInfo === 'function' ? process.getActiveResourcesInfo() : []);
  let stacksTaken = '';
  Object.defineProperty(globalThis, Symbol.for('gregale.profiling'), {value: Object.freeze({
    pausedSnapshot(tag) { stacksTaken = String(tag); return JSON.stringify(activeResources()); },
    closeInspector() { try { require('node:inspector').close(); } catch (_) { /* best effort */ } },
  })});
  // inspectorState tells guest-init how to reach this process's inspector:
  // already open on a port, openable with SIGUSR1 on process.debugPort, or
  // not at all. An application listening for SIGUSR1 is never signalled.
  function inspectorState() {
    try {
      const url = require('node:inspector').url();
      if (url) return 'open:' + new URL(url).port;
      const flags = process.execArgv.join(' ') + ' ' + (process.env.NODE_OPTIONS || '');
      if (flags.includes('--disable-sigusr1') || process.listenerCount('SIGUSR1') > 0) return '';
      return 'sigusr1:' + process.debugPort;
    } catch (_) { return ''; }
  }
  function stacksProfile() {
    const f = require('pprof-format');
    const strings = new f.StringTable();
    const counts = new Map();
    const active = activeResources();
    for (const type of active) counts.set(type, (counts.get(type) || 0) + 1);
    const functions = [], locations = [];
    const frame = name => {
      const id = functions.length + 1;
      functions.push(new f.Function({id, name: strings.dedup(name), filename: strings.dedup('')}));
      locations.push(new f.Location({id, line: [new f.Line({functionId: id})]}));
      return id;
    };
    const root = frame('<event loop>');
    const sample = [];
    for (const [type, n] of counts) sample.push(new f.Sample({locationId: [frame('waiting on ' + type), root], value: [n]}));
    const threads = new f.ValueType({type: strings.dedup('threads'), unit: strings.dedup('count')});
    return new f.Profile({sampleType: [threads], sample, location: locations, function: functions, stringTable: strings,
      periodType: threads, period: 1, timeNanos: Date.now() * 1e6});
  }
  async function uploadStacks(tag) {
    try { await upload('stacks', tag, Date.now(), await pprof.encode(stacksProfile())); } catch (_) { /* best effort */ }
  }
  // On-demand CPU uses the same time profiler as the continuous Pyroscope
  // collector, stopped explicitly so the window flushes at its end.
  async function stopCapture() {
    const c = capture; capture = null;
    if (!c) return;
    // Read the heap first, then stop CPU, and encode only afterwards: building
    // and encoding the CPU profile allocates, and must not show as app heap.
    let cpu = null, heap = null;
    try { if (c.heap && heapRunning) heap = pprof.heap.profile(); } catch (_) { /* best effort */ }
    try { if (c.cpu) cpu = pprof.time.stop(); } catch (_) { /* best effort */ }
    if (heapRunning) { pprof.heap.stop(); heapRunning = false; }
    if (cpu) await upload('cpu', c.epoch, c.from, await pprof.encode(cpu));
    if (heap) await upload('heap', c.epoch, c.from, await pprof.encode(heap));
  }
  function startCapture(config) {
    pprof = pprof || require('@datadog/pprof');
    const want = kinds(config);
    capture = {epoch: config.epoch, from: Date.now(), checkpointAt: Date.now(), cpu: want.includes('cpu'), heap: want.includes('heap')};
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
      const inspector = inspectorState();
      const config = await request('/control?pid=' + process.pid + (inspector ? '&inspector=' + encodeURIComponent(inspector) : ''));
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
        if (kinds(config).includes('stacks') && stacksTaken !== config.epoch) await uploadStacks(config.epoch);
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
      } else if (capture && capture.heap && heapRunning && config.capture && config.epoch === capture.epoch &&
                 config.checkpoint_seconds > 0 && Date.now() - capture.checkpointAt >= config.checkpoint_seconds * 1000) {
        // Interim heap for an OOM kill that would end the window early.
        capture.checkpointAt = Date.now();
        await upload('heap', capture.epoch, capture.from, await pprof.encode(pprof.heap.profile()), true);
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
