# ADR-20261117065818 · Interactive browser sessions for stateless agents (`gregale_browser`)

- **Status:** proposed (owner decision, 2026-11-17: "Go with 2, interactive browsing")
- **Date:** 2026-11-17
- **Amends (when accepted):** ADR-20261030125501 §4 (a fourth platform tool), ADR-20261116182615 §3 (actions on a live session beside one-shot tasks).
- **Builds on:** ADR-20261116182615 (browser sessions, sandbox tasks, `gregale_browse`), ADR-20261027090013 §4–§5 (session VMs, freeze and thaw), ADR-20261112121456 (sandbox `web` mode).

## Context

`gregale_browse` reads one page and ends its session. An agent cannot log in, follow a link it chose, fill a search box, or page through results. Other agent sandbox providers offer this, either through a browser the agent drives over CDP or through tools that act on a page.

A sandbox SDK user can already do this: `startBrowser()` and the CDP preview URL work with Playwright. A stateless agent (ADR-20261030125501) cannot. It holds no connection between invocations, and each tool call must end in a result published into its thread.

## Decision

### 1. `gregale_browser`, a stateful tool on a live browser session

- A fourth tool in the Gregale RAP toolset. Its arguments are `action` plus that action's fields:

  | action | fields | does |
  |---|---|---|
  | `open` | `url` | admits a browser session for the thread and loads `url` |
  | `navigate` | `session_id`, `url` | loads `url` in the session's page |
  | `click` | `session_id`, `ref` | clicks an element from the last snapshot |
  | `type` | `session_id`, `ref`, `text`, optional `submit` | focuses the element, replaces its value with `text`, and presses Enter when `submit` |
  | `press` | `session_id`, `key` | presses one key of a closed set (Enter, Tab, Escape, Backspace, arrows, PageUp, PageDown, Home, End) |
  | `scroll` | `session_id`, `direction` | scrolls the page by one viewport, `up` or `down` |
  | `back` | `session_id` | goes back one history entry |
  | `snapshot` | `session_id` | reads the page again without acting |
  | `close` | `session_id` | closes the session |

- Every action except `close` answers with the page's **snapshot**:
  - `url`, `title`, `status` (of the last document load).
  - `elements`: the visible interactive elements (links, buttons, inputs, selects, textareas, elements with an ARIA widget role), each `{ref, role, name}` plus `value` for inputs. At most 150 elements.
  - `text`: the page's visible text, cut to `max_chars` (default 4000).
- `ref` is a small integer that the snapshot stamps on its element as `data-gregale-ref`. A ref is valid until the next action, since every action takes a new snapshot. A stale or unknown ref is a tool error naming it, never a click elsewhere.
- Like `gregale_run` and `gregale_browse`, the tool needs the app's credential, because it spends the account's compute.

### 2. Actions travel on the session's stream

- apid records each action as a row of `sandbox_actions`: session, sequence number, kind, arguments (validated JSON), and the thread and tool call it answers. The tool call's ID is its dedupe key, so a retried invocation finds its action.
- agentd owns the session (CLAUDE.md, component ownership). Once a second, each replica reads the pending actions of the sessions it holds, in one query. It sends a session its next action as a `RunSandboxAction` frame on the session's existing `RunSandbox` stream: agentd → schedd → vmmd.
- vmmd validates the action against the closed set of kinds, thaws a frozen VM as a sandbox call does, and runs `gregale-browser act <kind> [flags] [-- <value>]` with the ordinary exec operation. The command is vmmd's constant; caller input reaches it only as validated arguments after `--`, never as shell text. It reports `RunSandboxActionDone {action_id, exit_code, output}`, with output capped at `SandboxTaskOutputMaxBytes`.
- agentd records the result on the action row under the session's lease, then sends the next pending action. A session runs one action at a time, because a page has one state.
- apid publishes each finished action into its thread as the tool call's result, the way it delivers a browse task's end. An inbox message holds at most 4 KiB. Text is cut first, then elements past the first 40; `result_url` (`GET /v1/sandboxes/{id}/actions/{seq}`) returns the whole snapshot.

### 3. The session's life

- `open` admits a session exactly as a browse task does (`browser: true`, network `web`, the plan's sandbox limits and concurrency), without a task. It is linked to the thread, so only that thread's calls can act on it.
- The session closes on `close`, after `SandboxBrowserIdleCloseSec` with no action (the session's existing idle timeout, which agentd already enforces: an action counts as a call), at its lifetime cap (`SandboxBrowserLifetimeSec`), or when an action's result finds the thread can no longer receive it (its callback URLs revoked). A thread closed with no action pending is left to the idle close.
- A running action whose session is claimed again (its agentd or VM was lost) ends without an exit code. So does every open action once the session ends. apid reports either as "the session ended".
- Between actions the session freezes after `SandboxIdleFreezeSec`, like any session. It is billed as sandbox compute (ADR-20261027090013 §5). The next action thaws it.
- `SandboxBrowserActionsMax` bounds the actions per session. Each action runs within `SandboxBrowserActionTimeoutSec`.

### 4. Safety

- Egress is the session's `web` mode: public hosts on ports 80 and 443, the egress denylist, and no RFC1918 or metadata ranges. `navigate` and `open` URLs are validated like `gregale_browse`'s.
- `type` text is at most 2048 characters and travels as one argv entry. It is typed through CDP `Input.insertText`, never evaluated as script.
- The tool takes no cookies, no headers, and no file uploads. Downloads are refused (`Browser.setDownloadBehavior deny`).
- Chromium keeps the profile only for the session's life. It is deleted with the VM.

### Limits (`pkg/api/limits.go`)

| Limit | Free | Hobby | Pro | Scale |
|---|---:|---:|---:|---:|
| `SandboxBrowserIdleCloseSec` | 0 | 300 | 600 | 600 |
| `SandboxBrowserLifetimeSec` | 0 | 1800 | 3600 | 3600 |
| `SandboxBrowserActionsMax` | 0 | 200 | 500 | 500 |
| `SandboxBrowserActionTimeoutSec` | 0 | 30 | 30 | 30 |

## Rollout

- **PR A:** `sandbox_actions` (expand migration), `gregale-browser act`, the `RunSandboxAction` frames through schedd and vmmd, and agentd's action pump.
- **PR B:** the `gregale_browser` tool, action delivery into threads, and `GET /v1/sandboxes/{id}/actions/{seq}`. `open` admits the session and its first action (a `navigate`) in one transaction, so a retried open finds its session.
- An older schedd or vmmd ignores an action frame it does not know. agentd sends actions only to a session whose vmmd announced action support (`RunSandboxBooted.actions`). apid admits `open` only behind `FAAS_SANDBOX_BROWSER` and `FAAS_SANDBOX_WEB_NETWORK`.
- A metal check of a click and type round trip, including one across a freeze and thaw, comes before the switches are turned on.

## Not yet

- **Screenshots.** A PNG does not fit an inbox message, and agents would need to fetch it. A follow-up stores it in the artifact store behind the action's `result_url`.
- **Several tabs per session.** One page per session keeps refs and state simple.

## Rejected alternatives

- **Hand the agent a CDP URL.** A stateless agent holds no WebSocket between invocations, and every runtime would need a CDP client.
- **apid runs actions over the sandbox data plane.** apid would bypass the session's owner (agentd), which ADR-20261116182615 also rejects.
- **A new session per action, replaying the history.** It is slow, and pages with state (logins, carts) do not replay.
- **CSS selectors or XPath as targets.** Models guess them wrong, and they open script-like input; refs from the snapshot are exact and closed.
