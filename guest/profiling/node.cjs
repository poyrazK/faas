// Managed Node CPU profiling bootstrap. Failures affect diagnostics only.
'use strict';
if (process.env.FAAS_PROFILING_ENABLED === '1') {
  const http = require('node:http');
  let profiler, epoch = '', running = false;
  const endpoint = process.env.FAAS_PROFILING_ENDPOINT || 'http://127.0.0.1:9191';
  const request = (path, method = 'GET') => new Promise((resolve, reject) => {
    const req = http.request(endpoint + path, {method, timeout: 200}, res => {
      let data = '';
      res.on('data', chunk => { data += chunk; if (data.length > 4096) req.destroy(); });
      res.on('end', () => {
        if (res.statusCode >= 300) return reject(new Error('profile control unavailable'));
        try { resolve(data ? JSON.parse(data) : {}); } catch (err) { reject(err); }
      });
    });
    req.on('error', reject); req.on('timeout', () => req.destroy()); req.end();
  });
  async function tick() {
    try {
      const config = await request('/control?pid=' + process.pid);
      if (running && (config.suspended || config.epoch !== epoch || !config.enabled)) {
        await profiler.stopWallProfiling(); running = false;
      }
      if (config.suspended) {
        await request('/control/ack?pid=' + process.pid + '&epoch=' + config.epoch, 'POST');
      } else if (config.enabled && !running) {
        profiler = profiler || require('@pyroscope/nodejs').default;
        profiler.init({serverAddress:endpoint, appName:'gregale',
          flushIntervalMs:config.window_seconds * 1000,
          wall:{samplingDurationMs:config.window_seconds * 1000, collectCpuTime:true, samplingIntervalMicros:10000},
          tags:{gregale_epoch:config.epoch, gregale_process:String(process.pid)}});
        epoch = config.epoch;
        profiler.startWallProfiling(); running = true;
      }
    } catch (_) { /* diagnostics never prevent the application from serving */ }
    setTimeout(tick, 100).unref();
  }
  tick();
}
