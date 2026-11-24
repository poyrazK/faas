# Coding-agent eval

Measures how well coding agents use Gregale. Each run:

1. scaffolds a starter into a fresh directory;
2. applies the task's setup (code edits and deploys);
3. starts the agent headless with the `gregale` CLI and, optionally, the
   management MCP server at one toolset;
4. checks the outcome: the app's HTTP response, its serving release, files the
   agent must not touch, or the agent's answer;
5. records success, turns, tool calls, tokens, cost and time;
6. deletes the run's app.

Tasks live in `tasks.json`:

| Task | What the agent must do |
|---|---|
| `deploy-starter` | Deploy a project and confirm it serves |
| `fix-failed-deploy` | Find why a deploy crashes (a missing import), fix it and redeploy |
| `rollback-bad-release` | Restore service after a release that returns 500, without editing files |
| `diagnose-failure` | Name the root cause of a failed deploy, changing nothing |
| `deploy-retail-store` | Deploy `aws-containers/retail-store-sample-app` (five services, Compose) from the owner's own words |
| `deploy-voting-app` | Deploy `dockersamples/example-voting-app` (Python, Node, .NET worker, Redis, Postgres) from the owner's own words |
| `deploy-express-render` | Deploy Render's Express example |
| `migrate-fastapi-render` | Move Render's FastAPI example; its start command is only in `render.yaml` |
| `deploy-flask-render` | Deploy Render's Flask example |
| `migrate-django-heroku` | Move Heroku's Django getting-started app; its `ALLOWED_HOSTS` opens only when Heroku's `DYNO` is set, so Gregale's smoke request gets 400 until the agent fixes it, and it asks for Python 3.15 |
| `deploy-go-heroku` | Deploy Heroku's Go getting-started app; its Procfile starts `go-getting-started`, a binary only Heroku's buildpack makes |

The five framework tasks follow the framework and migration guides
(`docs/frameworks.md`, `docs/deploy-*.md`, `docs/migrate-from-*.md`): each
passes when the app serves its page, and its features record whether the
agent checked first (`--dry-run`), diagnosed with Gregale, and made the
framework's fix in the repository. Run them for a baseline after a docs or
CLI change:

```sh
python3 scripts/agent-eval/agent_eval.py run --agents claude,codex --toolsets none \
  --tasks deploy-express-render,migrate-fastapi-render,deploy-flask-render,migrate-django-heroku,deploy-go-heroku \
  --gregale ./bin/gregale
```

### Foreign-repository tasks (ADR-20261112223000)

A task with `repo` (a github.com URL and a full commit SHA) instead of
`template` checks that commit out and gives the agent the prompt verbatim,
with `{repo_url}` filled in and nothing about Gregale added. These tasks
measure what a cold agent does with a repository Gregale did not create:

- The agent may run any shell command (Claude: plain `Bash`).
- Apps and projects that appear in the evaluation account during the run are
  the run's. Checks read them (`apps_created`, `apps_serving`, `app_http`
  on the created app whose slug matches, `no_traps`), and cleanup deletes
  them. `no_traps` grades the end state: an app still parked by this run, or
  a latest release that failed, not a failed release the agent replaced. Runs are sequential for that reason.
- `features` lists the Gregale features that apply to the repository. Each is
  a regex over the agent's shell commands (`command`), over files in the
  checkout after the run (`file`), or a fact about what it created
  (`state: project_created`). `feature_score` is the share used. A feature
  with `"unless": "first_deploy_live"` is a fix the agent needs only when
  Gregale's detection falls short; it counts as used when every app the run
  created went live on its first deployment. Until the
  scan reports features itself (ADR-20261112223000 §1), the list is written
  by hand.
- Every run is graded for anti-patterns in its shell commands: raw calls to
  the Gregale API, hand-built tarballs, deleting apps or projects, minting API
  keys, and one command repeated more than three times. `traps` lists created
  apps whose health or deployments show `liveness_exhausted` or
  `app_startup_timeout`.

## Run

Runs deploy and delete real apps. Use an API key of a dedicated evaluation
account, never your own login:

```sh
export GREGALE_EVAL_TOKEN=...                 # evaluation account API key
export GREGALE_EVAL_API=https://api.gregale.dev   # optional
make agent-eval AGENT_EVAL_TOOLSETS=core,all,none AGENT_EVAL_REPEAT=3
# or
python3 scripts/agent-eval/agent_eval.py run --agents claude,codex,gemini,cursor \
  --toolsets core,all,none --tasks deploy-starter,rollback-bad-release \
  --claude-model claude-sonnet-5-5 --gregale ./bin/gregale
python3 scripts/agent-eval/agent_eval.py summarize agent-eval-results.jsonl
```

- `--toolsets none` runs with the CLI only, so it shows what MCP adds.
- `--gregale` selects the binary under test. It serves MCP, and it is the
  `gregale` on the agent's `PATH`.
- Each task has a dollar budget (`budget_usd`, enforced by Claude's
  `--max-budget-usd`) and a timeout.
- Results append to `agent-eval-results.jsonl`, one line per run.
  `--keep-dirs` keeps transcripts; `--keep-apps` keeps apps for inspection.
- `--cli-login` runs with your own logged-in CLI instead of
  `GREGALE_EVAL_TOKEN`. Your account is shared with your other sessions, so an
  app or project counts as the run's only if the agent named it: in a command,
  a tool call, or a config file of the project (Compose service names,
  `gregale.yaml`). Others that appear during the run are listed as
  `not_attributed` and left alone. Cleanup deletes only the run's apps and
  projects that did not exist before the run, never one it redeployed.
- Cleanup purges each app it deletes (`gregale app purge`), because a deleted
  app holds its name for seven days and the next run's agent would otherwise
  find the repository's natural name taken. `left_behind` lists `app:<slug>`
  for an app it could not delete and `name:<slug>` for one whose name is
  still held.
- A task's `follow_up` is sent (Claude and Codex, by resuming the session)
  when the agent finished its turn but the task's checks don't pass yet, for
  example because it stopped to ask before deploying. It is sent up to twice:
  an agent may ask again, such as whether to make a private app public.
  `follow_ups` records how many were sent.
- `wrote_outside_run` lists files the agent's file tools wrote outside its run
  directory and adds that anti-pattern. `home_writes` lists files under your
  home directory, two levels down, that changed during the run (agent config
  and shell history are skipped). Other processes can show up there, so it's a
  report for you to check, not a grade.

## Agents

- **Claude Code**: `claude -p --output-format stream-json`.
  - Shell access is limited to `gregale` and `curl`, plus file tools and
    Gregale's MCP tools (`--permission-mode dontAsk`).
  - With `ANTHROPIC_API_KEY` set, each run uses a fresh `CLAUDE_CONFIG_DIR`, so
    your settings, memory and plugins stay out. Otherwise results are marked
    `"isolation": "user-config"`.
  - Cost, turns and tokens come from the result event.
- **Codex**: `codex exec --json --ignore-user-config`, sandboxed to the run
  directory with network access.
  - Tokens come from `turn.completed`; Codex reports no cost.
  - If your default model isn't available on your account, pass
    `--codex-model`.
  - A repository task keeps its session (no `--ephemeral`) so the follow-up
    can run `codex exec resume`. An old Codex CLI may reject every model on a
    ChatGPT account (`model_not_found`, 0.138 on 2026-11-19); upgrade it.
- **Gemini CLI**: `gemini -p --output-format stream-json --approval-mode yolo
  --skip-trust`.
  - Needs `GEMINI_API_KEY` (or `GOOGLE_API_KEY`, or Vertex AI): the personal
    Code Assist login no longer serves the CLI. With `GEMINI_API_KEY` each run
    uses a fresh `GEMINI_CLI_HOME`, so your `GEMINI.md` and settings stay out.
  - MCP comes from a run-local settings file (`GEMINI_CLI_SYSTEM_SETTINGS_PATH`).
  - Tokens come from the result event; Gemini reports no cost or turns, so
    turns are tool calls plus one.
- **Cursor**: `cursor-agent -p --output-format stream-json --force --trust`.
  - Needs `cursor-agent login` or `CURSOR_API_KEY`; the run checks first.
  - MCP comes from the checkout's `.cursor/mcp.json`, approved with
    `--approve-mcps`.
  - Tokens come from the result event; turns are distinct model calls.

The agent's environment drops the caller's agent markers (`CLAUDECODE`,
`GEMINI_CLI`, …), so an eval started from inside an agent does not look like
that agent to the CLI under test. Every parser records the shell commands the
agent ran (`commands`), which the graders read.

`make agent-eval-test` runs the harness's unit tests. It uses recorded
transcript shapes and starts no agent.

## First-hour tasks

`first-hour-private` grades a private app through `release verify`, retaining the
exact app/release/instance identity and actual application status.
`first-hour-edit` starts with a private deployment and grades the changed source
on a new release of the same app. Select these with `fix-failed-deploy` for the
first-hour agent lane. See [the acceptance and baseline guide](../../docs/first-hour-acceptance.md).
