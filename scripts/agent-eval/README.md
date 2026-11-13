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

### Foreign-repository tasks (ADR-20261112223000)

A task with `repo` (a github.com URL and a full commit SHA) instead of
`template` checks that commit out and gives the agent the prompt verbatim,
with `{repo_url}` filled in and nothing about Gregale added. These tasks
measure what a cold agent does with a repository Gregale did not create:

- The agent may run any shell command (Claude: plain `Bash`).
- Apps and projects that appear in the evaluation account during the run are
  the run's. Checks read them (`apps_created`, `apps_serving`, `app_http`
  on the created app whose slug matches, `no_traps`), and cleanup deletes
  them. Runs are sequential for that reason.
- `features` lists the Gregale features that apply to the repository. Each is
  a regex over the agent's shell commands (`command`), over files in the
  checkout after the run (`file`), or a fact about what it created
  (`state: project_created`). `feature_score` is the share used. Until the
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
