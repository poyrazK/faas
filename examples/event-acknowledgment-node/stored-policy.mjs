// Explicit opt-in staging for a worker reconstructed from a protected authenticated job.
// Upgrade the schema and every recovery host before using this path for new intents.
export async function stageCompletionWithStoredPolicy(worker, pool, handler, signal) {
  return worker.runDurable(pool, handler, {
    signal,
    retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 },
  });
}
