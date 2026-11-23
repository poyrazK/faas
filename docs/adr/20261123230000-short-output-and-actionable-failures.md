# ADR-20261123230000: Short output and failures that carry their own fix

Date: 2026-11-23
Status: Accepted
Related: ADR-20261112223000 (agents discover and use features; the agent pointer), ADR-20261123180000 (`gregale guide "<text>"`), ADR-110 amendment 1 (error explanations, `pkg/whycopy`)

## Context

The 2026-11-23 coding-agent eval (Claude and Codex, 25 runs, 259 `gregale`
commands) measured how agents read Gregale's output:

- 153 of the 259 commands (59%) were piped through `head`, `tail` or
  `grep` to cut the output down.
- The agent pointer ("Agent: run `gregale guide` …") printed once per
  *process*. Every `gregale` call is a new process, so one run showed it
  33 times.
- A project deploy printed every doctor check, including `— ok` rows and
  `— skipped` rows with their reasons; `env list` opened with a
  three-sentence note on what delivery records mean; a deploy that went
  live still printed "Saved project deploy …; resume with: …".
- The voting-app worker failed with `app_startup_crash`. The fix said
  `gregale logs <slug> --since 30m` although the deploy knew the slug, and
  "the build log does not have it". The cause (`permission denied for
  schema public`) was in the app's log, which the failure did not show.

## Decision

1. **The agent pointer shows at most once an hour.** A marker file in the
   user cache directory (`gregale/agent-hint`) records the last time it
   printed; it still prints at most once per process. Without a cache
   directory it falls back to once per process.
2. **Compact output, for everyone.** No stdout depends on agent detection,
   so ADR-20261112223000 §5 stands.
   - Deploy's doctor section lists findings only and counts the rest on
     one line ("N other check(s) passed, M skipped until the app is
     deployed; `gregale doctor` lists every check"). `gregale doctor`
     keeps the per-check list.
   - `env list`'s delivery overview is one line with the counts, plus the
     inspect command when something needs a look. The caveat about what
     delivery records mean moves to `env list --help`.
   - "Saved project deploy …; resume with …" prints only while a deploy is
     still in flight or has failed.
3. **Failures carry their own next step.**
   - `api.Problem` gains `fix_commands` (the runnable commands quoted in
     `fix`, in order) and `retryable` (true for 429, 502, 503, 504 or a
     Retry-After; false for other 4xx; absent otherwise). Both omitempty.
   - `whycopy.Complete` fills the placeholders a failure knows (`<slug>`,
     `<app-slug>`, `<app>` with the app; `<deployment-id>`, `<id>` with the
     deployment), then derives `fix_commands`. A command that still holds a
     placeholder (`<name>`, an upper-case word, `...`) is left out:
     `fix_commands` lists only commands that run as written.
     `whycopy.Decorate` completes every catalog problem.
   - The server fills what it knows: schedd's snapshot-prime failure fills
     the app slug and deployment ID before it stores the deployment's fix.
   - The CLI fills the rest from its own context. A failed project service
     shows its newest log lines (up to 10 shown, 20 kept, read within 3 s
     from the deployment's log, else the app's) and a fix with its slug and
     deployment filled in. The `--json` progress event carries
     `fix_commands` and `relevant_logs`. Every `--json` problem the CLI
     writes carries `fix_commands` and `retryable`, even from an older
     server.

## Consequences

- An agent reads a failure's cause and a command to run in the failure
  itself, instead of `gregale logs` plus a guess at the slug.
- People see the same shorter output; the full doctor list and the
  delivery caveat are one command away.
- The OpenAPI `Problem` schema and the generated Node and Python SDK models
  gain the two fields.
- Fix text in `pkg/whycopy` should keep runnable commands in backticks
  and use the placeholders above, so `Complete` can fill them.

## Follow-ups

- Other failure sites that know the app (imaged, builderd, the remaining
  schedd paths) can call `whycopy.Complete` with it.
- The server does not attach `relevant_logs` to deployments yet; the CLI
  reads them. Attaching them where the failure is detected would give the
  dashboard, email and MCP the same lines.
- Re-run the agent eval and compare the share of trimmed commands (59%)
  and pointer repeats (33 in one run) with this baseline.
