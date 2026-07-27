// test/helpers/free-port.ts — ask the kernel for a free TCP port.
//
// Mirrors `sdk/fakeapid/main_test.go::freePort` (which uses
// `net.Listen("tcp", "127.0.0.1:0").Addr().(*net.TCPAddr).Port`).
// Node's equivalent is `server.address()` after `listen(0)`. The
// TOCTOU race documented at the Go helper applies here too — the
// port may be taken between Close and the subprocess binding. CI
// is the only place we exercise the spawn flow; locally, races are
// vanishingly rare.

import { createServer } from 'node:net';

/** Return an unused TCP port on 127.0.0.1. The temporary listener
 *  is closed before the port is returned; the caller owns the
 *  port from then on. */
export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      if (addr === null || typeof addr === 'string') {
        reject(new Error('freePort: expected object address'));
        return;
      }
      const port = addr.port;
      srv.close((err) => {
        if (err) reject(err);
        else resolve(port);
      });
    });
  });
}