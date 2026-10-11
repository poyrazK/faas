import { createServer } from 'node:http';

// Gregale commits the returned transition. This handler has no database,
// bucket credentials, local persistent state, or external effects.
const server = createServer(async (req, res) => {
  if (req.url === '/healthz') {
    res.writeHead(200).end('ok');
    return;
  }
  if (req.method !== 'POST' || req.url !== '/__gregale/entities') {
    res.writeHead(404).end();
    return;
  }
  try {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const call = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    const count = call.state?.data?.count ?? 0;
    const alarm = call.event === 'alarm';
    const delta = alarm ? 1 : call.payload?.delta;
    if (call.protocol_version !== 1 || call.entity?.namespace !== 'counters' ||
        !Number.isSafeInteger(count) || !Number.isSafeInteger(delta) ||
        !Number.isSafeInteger(count + delta)) {
      res.writeHead(422).end('invalid counter transition');
      return;
    }
    const next = { count: count + delta };
    const alarmAt = alarm ? undefined :
      Object.hasOwn(call.payload, 'alarm_at') ? call.payload.alarm_at : call.state.alarm_at;
    if (alarmAt != null && (typeof alarmAt !== 'string' || !Number.isFinite(Date.parse(alarmAt)))) {
      res.writeHead(422).end('invalid alarm deadline');
      return;
    }
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ data: next, result: next, alarm_at: alarmAt }));
  } catch {
    res.writeHead(422).end('invalid entity envelope');
  }
});

server.listen(Number(process.env.PORT ?? 8080), '0.0.0.0');
