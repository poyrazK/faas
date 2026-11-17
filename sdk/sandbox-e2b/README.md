# @gregale/sandbox-e2b

An E2B-compatible client for Gregale sandbox sessions. Code written against the E2B SDK moves by changing the import and the API key; supported calls keep their names and signatures.

> Not published yet. Sandbox sessions are behind the internal agents capability until launch (ADR-20261027090013 §12). Written from E2B's public documentation; it copies no E2B code, and "E2B" is used only to describe compatibility.

```ts
import { Sandbox } from '@gregale/sandbox-e2b';

const sbx = await Sandbox.create({ timeoutMs: 300_000, ports: [3000] }); // GREGALE_API_KEY
await sbx.files.write('app/index.js', 'console.log("hi")');
const { stdout } = await sbx.commands.run('node app/index.js');
const server = await sbx.commands.run('npx serve -l 3000', { background: true });
console.log(`https://${sbx.getHost(3000)}`, sbx.previewToken(3000));
await server.kill();
await sbx.kill();
```

`docs/sandbox-migration.md` lists every call and difference. In short:

- `getHost(port)` answers only for exposed ports: pass `ports` to `create`, or `await sandbox.exposePort(port)`.
- Files inside `/workspace` use Gregale's files API; other paths go through commands and are capped at 256 KB.
- The default network mode is `registries`; `allowInternetAccess: false` is `none`.
- Unsupported calls (`pty`, stdin, `files.watchDir`, `pause`, MCP) throw `UnsupportedError` naming the call and the alternative.

Gregale options: `network`, `ramMb`, `lifetimeMs`, `ports`, `portVisibility`, `snapshotId`, `readyTimeoutMs`, `apiBase` (default `GREGALE_API_BASE`, then `https://api.gregale.dev`).
