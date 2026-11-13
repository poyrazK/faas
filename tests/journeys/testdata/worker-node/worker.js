// The e2e worker. It leases messages from its pull binding with the VM's
// workload-identity token, acks each one, and nacks one whose payload asks
// to fail. It has no dependencies so its source, and its build, never change.
const api = (process.env.GREGALE_API_URL || 'https://api.gregale.dev').replace(/\/$/, '');
const app = process.env.WORKER_APP;
const identity = process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT;
const queue = 'jobs';
let token = { value: '', until: 0 };
let stopping = false;
process.on('SIGTERM', () => { stopping = true; });

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

async function credential() {
  if (Date.now() < token.until) return token.value;
  const url = new URL(identity);
  url.searchParams.set('audience', 'gregale:queues');
  const response = await fetch(url);
  if (!response.ok) throw new Error(`workload identity: ${response.status}`);
  const minted = await response.json();
  token = { value: minted.access_token, until: Date.now() + Math.max((minted.expires_in || 300) - 60, 30) * 1000 };
  return token.value;
}

async function call(verb, body) {
  const response = await fetch(`${api}/v1/apps/${encodeURIComponent(app)}/queues/${queue}/leases${verb}`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${await credential()}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(30_000),
  });
  if (response.status === 401) token.until = 0;
  if (!response.ok) throw new Error(`lease${verb}: ${response.status} ${await response.text()}`);
  return response.json();
}

while (!stopping) {
  if (!app || !identity) {
    console.error('worker: WORKER_APP or FAAS_WORKLOAD_IDENTITY_ENDPOINT is not set');
    await sleep(5000);
    continue;
  }
  try {
    const { messages = [] } = await call('', { max_messages: 10, visibility_timeout_seconds: 30, wait_seconds: 20 });
    for (const message of messages) {
      const fail = Boolean(message.payload && message.payload.fail);
      if (fail) await call(':nack', { receipts: [message.receipt], error: 'asked to fail' });
      else await call(':ack', { receipts: [message.receipt] });
      console.log(JSON.stringify({ level: 'info', msg: fail ? 'nacked' : 'acked', id: message.id }));
    }
  } catch (error) {
    console.error(String(error));
    await sleep(2000);
  }
}
