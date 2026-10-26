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

## Run

Runs deploy and delete real apps. Use an API key of a dedicated evaluation
account, never your own login:

```sh
export GREGALE_EVAL_TOKEN=...                 # evaluation account API key
export GREGALE_EVAL_API=https://api.gregale.dev   # optional
make agent-eval AGENT_EVAL_TOOLSETS=core,all,none AGENT_EVAL_REPEAT=3
# or
python3 scripts/agent-eval/agent_eval.py run --agents claude,codex \
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

`make agent-eval-test` runs the harness's unit tests. It uses recorded
transcript shapes and starts no agent.
