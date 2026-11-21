import assert from 'node:assert/strict';
import { once } from 'node:events';
import test from 'node:test';
import { createApp } from '../app.js';

test('malformed and oversized JSON are redacted client errors in both protocols', async t => {
  const api = createApp({ version: 1, endpoint: '/mcp', transport: 'streamable-http',
    mode: 'stateless', legacy: true, allowed_origins: [], auth: { mode: 'open' },
  }, { log: () => {} });
  const server = api.app.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    server.closeAllConnections();
    await api.handler.close();
    await new Promise(resolve => server.close(resolve));
  });
  const endpoint = `http://127.0.0.1:${server.address().port}/mcp`;
  for (const protocol of ['2026-07-28', '2025-11-25']) {
    for (const [body, status, code] of [
      ['{"private":"HTTP_BODY_SECRET",', 400, -32700],
      [JSON.stringify({ private: 'HTTP_BODY_SECRET', value: 'x'.repeat(1 << 20) }), 413, undefined],
    ]) {
      const response = await fetch(endpoint, { method: 'POST',
        headers: { 'Content-Type': 'application/json', 'MCP-Protocol-Version': protocol }, body,
      });
      assert.equal(response.status, status);
      assert.equal(response.headers.get('cache-control'), 'no-store');
      const result = await response.json();
      assert.equal(result.error?.code, code);
      assert.equal(JSON.stringify(result).includes('HTTP_BODY_SECRET'), false);
      if (status === 400) assert.deepEqual(result, { jsonrpc: '2.0', id: null, error: { code: -32700, message: 'Parse error' } });
      else assert.deepEqual(result, { error: 'request_too_large' });
    }
  }
});
