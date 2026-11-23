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
- `Sandbox.create('code-interpreter')` boots the code interpreter: `runCode` runs Python in a Jupyter kernel and answers an `Execution` (results with `text`, `png`, `html`, …, logs, error, `executionCount`); `createCodeContext`, `listCodeContexts`, `restartCodeContext` and `removeCodeContext` manage kernels.
- `Sandbox.create({ envs })` sets the envs in the session once it runs (`PUT …/env`, kept in the session's memory only), so `Sandbox.connect` from another process sees them; `sandbox.setEnvs(envs)` replaces them.
- `pty.create` starts a login shell on a terminal as a background command; `sendInput`, `resize` and `kill` drive it, and `onData` receives its output, polled about every 200 ms. Background commands take input with `commands.sendStdin` and `closeStdin`; a foreground command takes none.
- `pause`/`betaPause` work on sessions created with `kind: 'persistent'` (memory and `/workspace` kept; `Sandbox.connect` resumes); `fork(n)` is Gregale-only. An ephemeral session freezes on its own instead.
- Unsupported calls (`files.watchDir`, `pause` on an ephemeral session, MCP) throw `UnsupportedError` naming the call and the alternative.

Gregale options: `network`, `ramMb`, `lifetimeMs`, `ports`, `portVisibility`, `snapshotId`, `readyTimeoutMs`, `apiBase` (default `GREGALE_API_BASE`, then `https://api.gregale.dev`).
