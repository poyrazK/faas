# ADR-20261128180713 · Sandbox browser: live view and takeover, browser profiles, and the actions API

- **Status:** proposed (owner decisions, 2026-11-28: "Top 3 first", live view "View + takeover", profiles "Account-scoped, object store", delivery "ADR, then one PR")
- **Date:** 2026-11-28
- **Amends (when accepted):** ADR-20261117065818 (new action kinds, actions outside agent threads, a human-control refusal), ADR-20261116182615 §1 (`browser` create options), ADR-20261027090013 §11 (a preview token mode).
- **Builds on:** ADR-20261112133503 (`gregale-browser` in agent-base), ADR-20261027090013 §11 (private ports and preview tokens) and step 7 (workspace persist and hydrate), ADR-20261122132055 (stored bytes billed per GB-month), ADR-20261110210000 (sealed captures, `crashcrypt`, keys by reference through secretd).

## Context

The sandbox browser runs Chromium in the session's microVM. SDK users drive it over CDP through a private preview port, and stateless agents drive it with `gregale_browse` and `gregale_browser` (snapshot, refs, screenshots). A feature review on 2026-11-28 compared it with dedicated browser products and found three gaps that matter most:

1. **No one can watch or step in.** A login, a 2FA prompt or a CAPTCHA stops an agent, and there is no way to see what the browser shows or to hand it to a person.
2. **Every session starts logged out.** Chromium's profile is deleted with the VM, so an agent logs in again on every session, which sites rate-limit and flag.
3. **The action set is narrow and only agent threads can use it.** There is no select, hover, wait or file upload, and only one tab: a link that opens a new tab leaves the agent on the old page. An app, MCP client or the CLI cannot use the snapshot-and-ref model at all, because actions exist only as `gregale_browser` tool calls.

## Decision

### 1. Live view and takeover

- **The viewer.** `gregale-browser` serves a live view on guest port **9223** (`SandboxBrowserLivePort`): a single static page plus a WebSocket. The page shows the active tab with CDP `Page.startScreencast` (JPEG, quality 60, at most `SandboxLiveViewFPS` frames a second, scaled to the 1280×800 viewport), its URL and title, and the tab list. It uses only Node built-ins and ships in agent-base, so no Gregale web app hosts it (faas-web is out of scope).
- **Getting a link.** `POST /v1/sandboxes/{id}/live {control?: bool}` exposes 9223 privately if it is not exposed and returns `{url, expires_at}`. The URL carries `?gregale_preview_token=`, so opening it once sets the host-only preview cookie (ADR-20261027090013 §11). It needs a browser session (`browser: true`) and the same credential as `POST …/ports`. The TS SDK adds `session.liveView({control})`.
- **View or control.** The preview token gains a mode claim, `view` or `control` (`PreviewClaims.Mode`, absent = control for tokens already issued). gatewayd-internal sets `Gregale-Preview-Mode` on the forwarded request after stripping every inbound `gregale-*` header, so the guest learns the mode only from the gateway. A `view` link can be shared without handing over the browser. The live port is refused on `public` visibility.
- **Taking control.** A `control` viewer shows a **Take control** button. Taking it writes a lease in the guest (`/run/gregale-browser/control`: holder, since) and lets the viewer send mouse, wheel, keyboard and paste events, which `gregale-browser` turns into `Input.dispatch*` and `Input.insertText` on the active tab. **Hand back** releases it. Only one viewer holds control; others watch. Control is released `SandboxLiveControlGraceSec` (60 s) after its viewer disconnects.
- **Agents pause while a person holds control.** `gregale-browser act` refuses while the lease is held, exit code 75, output `a person is controlling this browser in its live view; take a snapshot after they hand back`. vmmd reports it as the action's end like any failure, so the agent gets it as the tool result and the HTTP API as `409 browser_human_control`. Agents are not told the live URL (owner chose takeover without a link in tool results); the account holder opens it from the API or SDK.
- **Activity and billing.** An open viewer WebSocket counts as activity for the idle freeze and `SandboxBrowserIdleCloseSec`, like a long preview request. The session is billed as sandbox compute; viewing adds no other charge. At most `SandboxLiveViewersMax` viewers per session.

### 2. Browser profiles

- **What a profile is.** A named, account-scoped copy of Chromium's user-data directory: cookies, local and session storage, IndexedDB and saved site settings. Caches (`Cache`, `Code Cache`, `GPUCache`, `ShaderCache`, `GrShaderCache`, `Crashpad`) are left out.
- **API.**
  - `POST /v1/sandboxes` takes `browser_profile: {name, mode?}`, with `mode` either `read_write` (default) or `read_only`. It needs `browser: true`.
  - `GET /v1/browser-profiles` lists `{name, bytes, created_at, updated_at, last_used_at, in_use_by?}`; `DELETE /v1/browser-profiles/{name}` deletes one that no session holds.
  - A profile is created the first time a session saves it; a session naming a profile that does not exist starts with an empty one.
  - `gregale_browser` `open` takes an optional `profile` name, with the same rules.
  - The TS SDK: `createSession({browser: true, browserProfile: {name}})` and `client.browserProfiles.list()/delete()`. CLI: `gregale sandbox browser-profile list|delete`.
- **One writer.** At most one live session holds a profile `read_write`; another is refused `409 browser_profile_in_use`, naming the session. Any number may hold it `read_only`; they start from the last saved copy and save nothing.
- **Load and save.** Before `gregale-browser` starts, vmmd unpacks the profile into the profile directory, as a session from a `snapshot_id` gets its workspace (ADR-20261027090013 step 7). A `read_write` session saves it when the session closes cleanly, when `POST /v1/sandboxes/{id}/browser-profile:save` is called, and when a person hands back control in the live view (a login just happened). Each save stops Chromium's writes with CDP `Storage`/`Browser` flushes, archives the directory, and writes a new generation; the previous generation is deleted only after the new one is recorded, so a failed save keeps the last good copy. A session that fails or is lost saves nothing.
- **Reservation and storage.** A save reserves through agentd on the session's `RunSandbox` stream (`persist_requested` → `persist_grant`, kind `browser_profile`), like a workspace persist. The archive is written to `agents/browser-profiles/<account>/<profile id>/<generation>.tar.zst.age`. Profiles count toward `SandboxSnapshotBytesPerAccount` with workspaces.
- **Sealed at rest.** A profile holds login cookies, so it is sealed like crash captures (ADR-20261110210000): `crashcrypt` v2 with a per-generation key held by secretd and opened by reference for the session's vmmd only. Storage never sees plaintext, and no other account's session can open it.
- **Billing.** Stored profile bytes are metered hourly with session captures and volumes (`sandbox_storage_hours`, kind `browser_profile`) and billed per GB-month at the stored-bytes price (ADR-20261122132055 §6), so the open pricing decision covers profiles too. A profile unused for `SandboxBrowserProfileIdleDays` is deleted by agentd's sweep, after a notice 7 days before.
- **Limits.** `SandboxBrowserProfilesMax` per account and `SandboxBrowserProfileMaxMB` per profile. A save over the cap fails and keeps the last copy, with `browser_profile_too_large` in the session's events.

### 3. Actions: new kinds, tabs and an HTTP API

- **New kinds** (both `gregale_browser` and the HTTP API):

  | kind | fields | does |
  |---|---|---|
  | `select` | `ref`, `values` (≤ 20) | sets a `<select>`'s chosen options by value or visible label |
  | `hover` | `ref` | moves the mouse over the element (menus, tooltips) |
  | `wait` | one of `text`, `ref_gone`, `url_contains`, `load`, `ms` | waits, within the action's timeout, then snapshots |
  | `upload` | `ref`, `paths` (≤ 10, under `/workspace`) | sets a file input's files with CDP `DOM.setFileInputFiles` (HTTP API only: an agent thread has no workspace to put files in) |
  | `tab_open` | `url` | opens a tab and makes it active |
  | `tab_switch` | `tab` | makes a tab active |
  | `tab_close` | `tab` | closes a tab; the next one becomes active |

- **Tabs.** Every snapshot carries `tabs: [{tab, url, title, active}]`, at most `SandboxBrowserTabsMax` (10). `gregale-browser` records the active tab's target ID in `/run/gregale-browser/active-tab`; a page that opens a new tab (link `target=_blank`, `window.open`) makes it active, so the next action acts where the user would look. Further popups past the cap are closed and reported in the snapshot (`popups_blocked`).
- **The HTTP API.**
  - `POST /v1/sandboxes/{id}/actions {kind, …fields, max_chars?, screenshot?}` records an action and returns `202 {seq, state}`. It takes `Idempotency-Key`, so a retry records the action once.
  - `GET /v1/sandboxes/{id}/actions/{seq}?wait=<s>` already returns an action; `wait` (≤ 30) long-polls until it ends.
  - `GET /v1/sandboxes/{id}/actions` lists a session's actions.
  - It works on any running browser session of the account, not only those `gregale_browser` opened. A session that `gregale_browser` opened stays the thread's own: the HTTP API refuses it, so an app cannot act inside an agent's browser behind its back.
  - Credential: `deploy:write` (or `admin`) to act, the read surface to read. TS SDK: `session.browser.act({...})`, `session.browser.snapshot()`.
- **Storage.** `sandbox_actions.endpoint_id`, `thread_id`, `tool_call_id` and `issued_at` become nullable (expand); an API action has `idempotency_key` and no thread. Its end is not delivered to any thread; the delivery query skips rows without one.
- **URLs by network mode.** In `web` mode a navigate or `tab_open` URL must be a public host (`api.ValidSandboxBrowseURL`), as today. In `none` and `registries` mode it must be a loopback URL (`http://localhost:<port>`), so an SDK user can drive the app the session itself serves.
- **Guest commands.** vmmd still builds every command from validated fields; values travel after `--` as single argv entries. `select` values and `wait` text are capped at 256 characters each, `upload` paths must be clean paths under `/workspace` with no `..`.

### Limits (`pkg/api/limits.go`)

| Limit | Free | Hobby | Pro | Scale |
|---|---:|---:|---:|---:|
| `SandboxLiveViewersMax` | 0 | 2 | 5 | 10 |
| `SandboxLiveViewFPS` | — | 5 | 10 | 10 |
| `SandboxLiveControlGraceSec` | — | 60 | 60 | 60 |
| `SandboxBrowserProfilesMax` | 0 | 5 | 50 | 200 |
| `SandboxBrowserProfileMaxMB` | 0 | 100 | 250 | 500 |
| `SandboxBrowserProfileIdleDays` | — | 30 | 90 | 90 |
| `SandboxBrowserTabsMax` | — | 10 | 10 | 10 |

## Rollout

All dark behind `FAAS_SANDBOX_BROWSER` plus one switch per part, in one PR built in slices:

1. **Actions:** new kinds, tabs and the active-tab file in `gregale-browser`; vmmd validation; the expand migration; `POST/GET …/actions`; `gregale_browser` gains the kinds. Switch `FAAS_SANDBOX_BROWSER_ACTIONS_API`.
2. **Live view:** the viewer and control lease in `gregale-browser`; `PreviewClaims.Mode` and `Gregale-Preview-Mode` in gatewayd-internal; `POST …/live`; the act refusal; viewer activity. Switch `FAAS_SANDBOX_BROWSER_LIVE`. Order: agent-base, then gatewayd-internal, then apid.
3. **Profiles:** the `sandbox_browser_profiles` table (expand), apid routes, agentd reservation and sweeps, vmmd load/save with `crashcrypt` and secretd keys, metering kind. Switch `FAAS_SANDBOX_BROWSER_PROFILES`. Order: migration, secretd, vmmd, agentd, apid.
4. **Clients:** TS SDK, CLI, docs.

Older components refuse what they do not know: an older vmmd answers an unknown action kind with its "kind must be" refusal, and apid admits profiles and live links only behind their switches, after vmmd and gatewayd are released. Metal checks before each switch: a takeover round trip with an agent action refused while held; a profile saved and loaded across two sessions with a login cookie surviving; a popup making a new tab active; an upload.

## Open questions

1. **Price.** Profiles bill at the stored-bytes price that is still undecided (ADR-20261122132055 §6).
2. **Agent-visible live links.** Not now (owner choice). A later amendment could let `gregale_browser` return a `view` link for a person to open.

## Rejected alternatives

- **A VNC server and noVNC.** A real display server in agent-base, more RAM per session, and a second input path besides CDP. Screencast needs nothing new in the image.
- **The live view on the CDP port (9222).** Whoever holds a CDP link can read every cookie, so a viewer link would carry full control. A separate port with a `view` mode can be shared.
- **Profiles inside persisted workspaces.** Workspaces are not sealed and have no single-writer rule; a profile is a credential and needs both.
- **Agents queueing actions while a person holds control.** The action timeout is 30 s and a person takes minutes; a clear refusal lets the agent decide to wait.
- **Thread-scoped profiles.** Considered and not chosen: the owner chose account-scoped names, which agents name explicitly.
