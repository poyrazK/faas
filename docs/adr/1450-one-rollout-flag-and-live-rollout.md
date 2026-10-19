# ADR-1450 · One rollout flag and a live rollout view

- **Status:** proposed
- **Date:** 2026-10-16
- **Amends:** [ADR-1446](1446-safe-releases-by-default.md) (release policy), [ADR-1321](1321-evidence-paced-default-rollouts.md) (`--safe`)
- **Decision:** `gregale deploy --rollout safe|fast|immediate` is the one way to choose how a release reaches traffic, and the app's `release_policy` accepts the same three words as its stored default. At a terminal, `gregale deploy` follows a canary rollout live to 100%; Ctrl-C detaches and leaves the rollout running. `--safe`, `--canary-preset` and `--canary-stages` keep working for existing scripts and are marked deprecated; `--traffic-percent`, `--no-traffic` and `--rollback-on-5xx` stay, because they set a split, a dark deployment and the rollback opt-out rather than a rollout speed.
- **Why:** `gregale deploy` has about 71 flags, and six of them overlap on how a release rolls out (`--canary-preset`, `--canary-stages`, `--safe`, `--traffic-percent`, `--no-traffic`, `--rollback-on-5xx`). Since ADR-1446 and ADR-1321, the default is already the right choice for almost every release, so the question a person asks is "safe, fast or now?". And `deploy` returned at 1%, so the person had to run `deployment wait --rollout` to see the release they had just shipped finish.
- **Rejected alternatives:** Removing the old flags (they are in CI scripts and the GitHub Action; removal needs a major CLI version). Following the rollout in CI as well (it would lengthen every pipeline by the rollout's minutes without a person watching; `--rollout safe` waits on purpose). A `--rollout custom` that takes a ladder (custom ladders are rare and `--canary-stages` still serves them).

## Decision

### `--rollout`

| `--rollout` | Rollout | Waits for 100% |
|---|---|---|
| `safe` | the adaptive health-gated canary (ADR-1321) with first-wake 5xx rollback | always |
| `fast` | the `aggressive` ladder (5% → 50% → 100%, a minute each), with the server's default first-wake 5xx rollback | at a terminal |
| `immediate` | all traffic at once (canary `none`), with the server's default first-wake 5xx rollback | no canary to wait for |

`--rollout safe` is what `--safe` was. `--rollout` cannot be combined with `--safe`, `--canary-preset`, `--canary-stages`, `--traffic-percent` or `--rollback-on-5xx`. `--no-traffic` (dark deployments) stays: it is not a rollout speed but a staging mode. Without `--rollout`, the app's `release_policy` decides, as before.

### `release_policy`

The stored default accepts `safe`, `fast` and `immediate`. `fast` makes the ADR-1446 default fill the `aggressive` ladder instead of `adaptive`, with the same first-wake rollback and probes. `gregale app <slug> --release-policy fast` sets it. This is the "config file" home for the rollout choice: the app manifest, next to `release_probe_paths` (ADR-1449).

### Following the rollout

When stdout is a terminal, the session is not `--non-interactive`, and the deployment goes live with a canary, `gregale deploy` keeps printing the rollout's progress and decisions (ADR-1447) until it completes or aborts, then prints the usual receipt. Ctrl-C prints how to follow it again and exits 0: the rollout runs on Gregale, not in the CLI. In CI, under `--json`, or with `--no-wait`, deploy returns at the first stage as before.

### The older flags

`--safe`, `--canary-preset` and `--canary-stages` keep their behaviour; their help says "deprecated: use --rollout", and the docs lead with `--rollout`. `--canary-stages` remains the way to run a custom ladder. The other three are not rollout speeds and stay as they are: `--traffic-percent` sets a manual traffic split (`gregale traffic set` adjusts it later), `--no-traffic` stages a dark deployment, and `--rollback-on-5xx=false` opts one deploy out of the first-wake rollback.

## Consequences

- One flag covers the common choices; scripts keep working.
- A person deploying at a terminal sees the release reach 100% (about 4 minutes on the default) or abort, without a second command.
- `release_policy` gains a value; clients that enumerate it (SDKs) are regenerated.

## Rollout

Ship the flag, the policy value and the follow view together. CLI tests cover the mapping, the conflicts and the detach exit; the default-policy test covers `fast`.
