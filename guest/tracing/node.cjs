// Managed Node request tracing bootstrap (ADR-829). Failures affect
// diagnostics only and never prevent the application from starting.
'use strict';
function resolvable(specifier) {
  try { return require.resolve(specifier) !== ''; } catch (_) { return false; }
}
if (process.env.FAAS_TRACING_ENABLED === '1' && resolvable('@opentelemetry/sdk-node')) {
  try {
    // NodeSDK.start() installs both the CommonJS require hook and the ESM
    // loader hook, so CommonJS and ESM applications are covered.
    const { NodeSDK } = require('@opentelemetry/sdk-node');
    const { getNodeAutoInstrumentations } = require('@opentelemetry/auto-instrumentations-node');
    const { OTLPTraceExporter } = require('@opentelemetry/exporter-trace-otlp-proto');
    const sdk = new NodeSDK({
      // The exporter reads OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, which
      // guest-init points at the local bridge.
      traceExporter: new OTLPTraceExporter(),
      instrumentations: [getNodeAutoInstrumentations({
        // Filesystem, DNS and socket spans are high volume and say little
        // about request latency regressions.
        '@opentelemetry/instrumentation-fs': { enabled: false },
        '@opentelemetry/instrumentation-dns': { enabled: false },
        '@opentelemetry/instrumentation-net': { enabled: false },
      })],
    });
    sdk.start();
    // Flush on a natural exit only. A SIGTERM listener would replace Node's
    // default terminate-on-SIGTERM and stall the guest's graceful stop.
    process.once('beforeExit', () => { sdk.shutdown().catch(() => {}); });
  } catch (_) { /* diagnostics never prevent the application from serving */ }
}
