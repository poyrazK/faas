# @gregale/agents-sandbox

Gregale sandbox sessions as an [OpenAI Agents SDK](https://github.com/openai/openai-agents-js) sandbox backend. Your agent's model calls stay in your process; its commands and files run in a Firecracker microVM on Gregale.

> Not published yet. Sandbox sessions are behind the internal agents capability until launch (ADR-20261027090013).

```ts
import { run } from '@openai/agents';
import { Manifest, SandboxAgent, shell, filesystem } from '@openai/agents/sandbox';
import { GregaleSandboxClient } from '@gregale/agents-sandbox';

const agent = new SandboxAgent({ name: 'triage', instructions: '…', capabilities: [shell(), filesystem()] });
const client = new GregaleSandboxClient({ network: 'registries' }); // GREGALE_API_KEY
const manifest = new Manifest({ entries: { repo: { type: 'git_repo', repo: 'owner/name', ref: 'refs/pull/42/head' } } });
const result = await run(agent, 'Triage owner/name#42.', { sandbox: { client, manifest } });
```

`examples/pr-triage.ts` is the full example.

## Mapping

| Agents SDK | Gregale |
|---|---|
| `create({ manifest, options })` | `POST /v1/sandboxes`, wait until `ready`, then one `workspace:hydrate` for `file`, `dir`, `local_file` and `local_dir` entries and a `git fetch` in the guest for each `git_repo` |
| `exec` | `POST …/exec` |
| `execCommand` (the shell tool) | a background `POST …/exec`, polled for up to `yieldTimeMs`; a command still running keeps a session ID |
| `writeStdin` | polls that command's output. Writing input is refused: Gregale commands take no stdin yet |
| `readFile`, `listDir`, `pathExists`, `directoryExists`, `createEditor` | `…/files`, `…/files:list`, `…/files:stat`, `…/files:remove` inside `/workspace`; reads and existence checks outside it go through a command |
| `persistWorkspace`, `hydrateWorkspace` | a tar built in the guest and downloaded; `…/workspace:hydrate` |
| `session.persistSnapshot()` (Gregale only) | `…/workspace:persist`; create with `options.snapshotId` to start from it |
| `resolveExposedPort` | `POST …/ports`. A private port's endpoint carries its preview token in `query`; it is never written to the session state |
| `running`, `stop`, `shutdown`, `close`, `delete` | `GET` / `DELETE /v1/sandboxes/{id}` |
| `serializeSessionState` | `{ sandboxId, apiBase }` only. `resume` re-mints the session token with your API key, and refuses state for another API origin |

## Options

`preset` (`codex`, `opencode`, `claude-code`), `ramMb`, `network` (`none`, `registries`, `web`), `idleTimeoutS`, `lifetimeS`, `labels`, `tenant`, `snapshotId`, `portVisibility`, `readyTimeoutMs`, plus `apiKey` (default `GREGALE_API_KEY`) and `apiBase` (default `GREGALE_API_BASE`, then `https://api.gregale.dev`).

## Not supported yet

Mount entries, manifest users, groups and path grants, a manifest `root` other than `/workspace`, `runAs` other than `app` or `root`, terminal (`tty`) commands, stdin, and core SDK snapshots (use `persistSnapshot` and `snapshotId`). Each fails at `create`, or at the call, with an error that names it.
