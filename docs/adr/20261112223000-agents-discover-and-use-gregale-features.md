# ADR-20261112223000: Coding agents discover and use the features a repo needs

Date: 2026-11-12
Status: Proposed
Related: ADR-20261026110240 (agent docs, AGENTS.md, Claude Code plugin), ADR-20261026152009 (MCP toolsets and agent eval), ADR-20261107170000 (API ground rules), ADR-20261106105015 (gregale.yaml)

## Context

On 2026-11-12 Gemini CLI was asked, with no other context: "Deploy
https://github.com/aws-containers/retail-store-sample-app into gregale via
gregale cli in this mac, use my already authed account for it". It got the
five services live, then reported as missing features that `main` already
has:

| Reported missing | Already on main |
|---|---|
| Use Dockerfile `HEALTHCHECK` / Compose `healthcheck:` | Both parsed; an image check replaces the plan-default `/healthz` liveness probe |
| Compose vendor extensions | `x-gregale-service-policy`, `-allow-callers`, `-allow-call-scopes`, `-app-protocol`, `-service-reliability`, `-renamed-from`, `-env-import` |
| `.gregaleignore` | Exists; `.dockerignore` is honoured |
| `project deploy --only` | `deploy --only / --exclude / --persist-exclude` |
| `--no-canary` | `deploy --rollout immediate\|fast\|safe`, app release policy |
| Dry run | `deploy --dry-run` |
| Project-wide log stream | `logs --project <slug> --follow` |
| Project rollback | `project rollback`, `deploy --atomic --verify` |
| Shared project env | secrets shared by an environment's services (ADR-1139), `env ref` |
| `gregale.yaml` | Canonical config file |
| Local ↔ remote mesh | `dev bridge --dependencies --bind-env` |

It also fell back to raw `multipart/form-data` calls against
`/v1/apps/{slug}/deployments` because `liveness_probe` is API-only: the CLI
has `--healthcheck-path` (startup readiness) but no liveness flag, and
neither `gregale.yaml` nor Compose maps to it.

The agent surfaces of ADR-20261026110240 and ADR-20261026152009 did not
reach it:

- **AGENTS.md** is written only into starters made by `gregale init`; this
  was someone else's repository.
- **The Claude Code plugin and hosted MCP** need `gregale mcp connect` or a
  plugin install first. Gemini had neither.
- **`docs/agents.md` and `llms.txt`** are read only by an agent that goes
  looking.
- **The CLI**, the one surface every agent touches, has about 4,800 command
  and flag entries in `cli_meta.go`, no task index, no link from what the
  scan found in this repo to the feature that handles it, and errors whose
  hints do not always name an existing command.
- **`scripts/agent-eval`** runs Claude Code and Codex on the `hello-node`
  starter only. It measures whether a deploy succeeds, not whether the agent
  used the features that apply, so this failure mode was invisible.

## Decision

The CLI's own output teaches the agent at the moment a feature is relevant,
CI keeps that output true, and an eval on foreign repositories measures it.
Nothing is written into the user's repository unless they ask.

### 1. The scan says which features apply to this repo

`gregale deploy --dry-run`, `gregale project init` and `gregale doctor` gain a
`features` section (human output) and a `features` array (`--json`). Each
entry:

```json
{"id": "compose-healthcheck", "evidence": "docker-compose.yml:41",
 "applied": "liveness uses the Compose healthcheck, not /healthz",
 "change": "edit healthcheck: in docker-compose.yml",
 "docs": "https://gregale.dev/docs/health-checks"}
```

`applied` says what Gregale will do, and `change` names the exact command,
flag, `gregale.yaml` key or Compose key that alters it. Rules live in one
registry (`pkg/featurehints`), fed by `reposcan` and `frameworkprofile`
results, never by running customer code. First rules, taken from the
retail-store run:

- Compose `healthcheck:` or Dockerfile `HEALTHCHECK` found → used for liveness.
- Spring Boot with actuator → `/actuator/health` (inference added with the rule).
- Health path defaulted, not declared or inferred → warn that `/healthz` will
  be probed, naming `hosting.health` and `--healthcheck-path`.
- Services with no published port → private; `logs --project`, `dev bridge`.
- Images Gregale offers managed (`postgres`) → `gregale.yaml` `create`; images
  it does not (`redis`, `rabbitmq`) → run as a project service, said plainly.
- `.env*` files in the upload set → `.gregaleignore`.
- More than one deployable service → `deploy --only`, `--rollout`.

`TestEveryHintNamesARealSurface` fails when a rule's `change` names a command
or flag missing from `cli_meta.go`, or a key missing from the `gregale.yaml`
or Compose schema.

### 2. Every failure names its fix

Every deploy and runtime failure code in `pkg/api/errors.go` (and runtime
park reasons such as `liveness_exhausted`, `app_startup_timeout`) carries a
`fix` in its Problem: a command the agent can run or a key it can set.
`TestEveryFailureCodeHasAFix` requires a `fix` or an explicit `fix: none`
with a reason, and checks the command exists in `cli_meta.go`. The status
commands (`release list`, `project get`, `status`) show a parked serving
release as parked with its reason, not as `live`.

### 3. Nothing a customer configures is API-only

A CI gate compares the request fields of the customer deploy and app-update
operations in `api/openapi.yaml` with CLI flags, `gregale.yaml` keys and
Compose `x-gregale-*` keys. Each field must map to at least one, or appear in
`docs/cli-api-parity-exemptions.txt` with a reason. Like the ADR duplicate
baseline, today's gaps are frozen there (first entry: `liveness_probe`) and
the file may only shrink. A new API-only field fails CI.

### 4. `gregale guide` is the task index

`gregale guide` lists tasks; `gregale guide <task>` prints one, at most 4 KB:
`deploy`, `compose-project`, `health-checks`, `private-services`,
`secrets-and-env`, `rollouts`, `debug-a-failure`. Each page is generated from
`cli_meta.go`, the hint registry and short hand-written notes, so it cannot
name a command that does not exist. `--json` returns the same as structured
data. The generated pages are also written to `docs/agent-guide/` for the
docs site's `llms.txt` (built in faas-web, outside this repo).

### 5. The CLI notices an agent and points at the guide, without changing behavior

When a known agent marker is set (`CLAUDECODE`, `GEMINI_CLI`, and the
Codex and Cursor agent markers, each confirmed against that agent's current
release in slice 4; the list lives in one table), the CLI:

- adds one line at the top of `--help` and usage-error output:
  "Agent: run `gregale guide` for a task index; `gregale deploy --dry-run`
  lists the features this repo uses.";
- appends the same pointer to the `hint` of a Problem on stderr.

It never changes defaults, prompts, or stdout of a successful command, so
JSON parsing is unaffected. `GREGALE_NO_AGENT_HINT=1` turns it off.

### 6. The eval measures feature use on foreign repositories

`scripts/agent-eval` gains:

- **Agents** `gemini` and `cursor` beside `claude` and `codex`; Claude Code
  runs both with and without the plugin.
- **Foreign-repo tasks** pinned to a commit: `aws-containers/retail-store-sample-app`
  first, then a Spring Boot app, a Next.js app and a Python worker. The
  prompt is the user's own wording, with no Gregale hints added.
- **Graders**, beside today's outcome checks:
  - every service serves (HTTP check per public route; private ones through
    the eval account);
  - feature use: of the hints `deploy --dry-run` reports for the repo, how
    many the transcript's commands used or accepted;
  - anti-patterns: raw `curl` or HTTP calls to the Gregale API, hand-built
    tarballs, delete-and-recreate of an app, more than three retries of one
    failing command;
  - traps: any `liveness_exhausted` or `app_startup_timeout` during the run.

A baseline is recorded before slices 1–5 ship, and each slice reports its
change against it.

### Order

0. Eval agents, foreign-repo tasks and graders; record the baseline.
1. Feature hints in the scan, with the retail-store rules.
2. Failure `fix` fields and parked-status display.
3. Parity gate with the frozen exemption list.
4. `gregale guide` and agent-marker hints.

Fixing the gaps the parity gate freezes (the liveness flag first) is
separate work, tracked by the exemption list shrinking.

## Consequences

- An agent that only runs the CLI learns, from `deploy --dry-run`, which
  Gregale features handle this repository and how to change each one.
- The agent's next step after a failure is a real command, not a telemetry search.
- API-only configuration cannot grow without CI noticing.
- Agent success becomes a number per agent and repo, so doc, hint and tool
  changes can be judged.
- `--help` and errors look slightly different under an agent; output on
  success does not.
- Eval runs cost model and compute money; foreign-repo tasks run on demand
  and before a release, not on every PR.

## Rejected alternatives

- **Write `AGENTS.md` into any repo the CLI deploys.** It changes a user's
  repository without being asked. `project init --agents-md` may offer it
  later as an explicit opt-in.
- **Rely on MCP and the plugin.** They help agents already connected; this
  agent was not, and most first contacts will not be.
- **A longer `--help`.** More text an agent must read before it knows which
  part matters; hints tied to the repo say only what applies.
- **Change defaults when an agent is detected** (for example, force
  `--json`). Same commands would behave differently for people and agents,
  and detection can be wrong; the owner chose hint-only.
- **Fix only the liveness flag.** That closes this gap, not the next
  API-only field or the next feature an agent cannot find.
