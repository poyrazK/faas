# The CLI command tree

Gregale's CLI names every command the same way: a singular noun, an optional
subnoun, then a standard verb.

```
gregale <noun> [<subnoun>] <verb> [<target>] [flags]

gregale cron create --app my-api --schedule "0 * * * *" --path /tick
gregale release get 42
gregale env set --app my-api DATABASE_URL=postgres://...
gregale app scale my-api --ram 512
```

The earlier names keep working. Scripts, CI workflows and agents that call
`gregale crons add` or `gregale deploys status` run exactly as before.

## What changed

- **Nouns are singular.** `cron`, `release`, `app`, `domain` and `webhook`
  replace `crons`, `deploys`, `apps`, `domains` and `webhooks`.
- **Verbs are standard.**
  - `list`, `get`, `create`, `update` and `delete` act on records.
  - `set` and `unset` change settings.
  - `info`, `show`, `add` and `rm` still run as the standard verb.
- **Related commands share a noun.** For example:
  - `release` holds deployments, rollouts, canaries, traffic splits, approvals and freezes.
  - `env` holds an app's variables and secrets.
  - `alert` holds rules, channels, incidents, SLOs and checks.
  - `api` holds routes, the OpenAPI spec, consumers and tenants.
- **`app` takes the verb first.** `gregale app scale my-api` replaces
  `gregale app my-api scale`. The app can also come from `--app` or from the
  linked checkout.
- **One flag per concept.** `--env`, `--release`, `--cursor`, `--since`,
  `--until` and `--follow` work on every command that takes that concept.
  The earlier spellings (`--scope`, `--deployment`, `--after`, `--from`, …)
  keep working.
- **A few daily verbs stay at the top level:** `init`, `dev`, `deploy`,
  `logs`, `status`, `open`, `rollback`, `run`, `invoke`, `add`, `link`,
  `login`, `start` and `menu`.

Run `gregale help --all` to see every noun by family, and `gregale <noun>` to
see its verbs.

## What you see

- Help, shell completion and the [CLI reference](cli-reference.md) use the new names.
- Run an earlier name in a terminal and the CLI prints one line on stderr
  naming the new one:

  ```
  Tip: 'gregale crons list my-api' is now 'gregale cron list my-api'; the earlier name keeps working.
  ```

- `--json` and `--non-interactive` runs never print the tip, so scripts that
  parse output are unaffected.
- `gregale help --all --operator` lists every earlier name with the command
  that now holds it.

## For scripts and agents

Earlier names stay accepted for at least twelve months, and removing any of
them takes its own announcement. Move scripts to the new names when you next
touch them. The tables below map each earlier name.

<!-- BEGIN GENERATED: earlier names (TestCliTreeMigrationGuideFresh) -->

## Earlier commands

| Earlier | Now |
|---|---|
| `gregale alerts` | `gregale alert` |
| `gregale analytics` | `gregale metric requests` |
| `gregale approvals` | `gregale release approval` |
| `gregale apps` | `gregale app list` |
| `gregale audit-events` | `gregale audit` |
| `gregale automations` | `gregale automation` |
| `gregale bindings` | `gregale app binding` |
| `gregale cache` | `gregale edge cache` |
| `gregale canary` | `gregale release canary` |
| `gregale capabilities` | `gregale feature list` |
| `gregale channels` | `gregale alert channel` |
| `gregale chaos` | `gregale test` |
| `gregale connect` | `gregale github` |
| `gregale consumers` | `gregale api consumer` |
| `gregale context` | `gregale config` |
| `gregale cors` | `gregale edge cors` |
| `gregale crons` | `gregale cron` |
| `gregale customer-operations` | `gregale operation` |
| `gregale dashboards` | `gregale dashboard` |
| `gregale delayed-task` | `gregale cron create --at` |
| `gregale deliver` | `gregale webhook deliver` |
| `gregale deploy-tokens` | `gregale app deploy-token` |
| `gregale deployment` | `gregale release` |
| `gregale deployments` | `gregale release list` |
| `gregale deploys` | `gregale release` |
| `gregale diff` | `gregale environment diff` |
| `gregale doctor` | `gregale deploy check` |
| `gregale domains` | `gregale domain` |
| `gregale edge-rule-lists` | `gregale edge rule-list` |
| `gregale edge-rules` | `gregale edge rule` |
| `gregale events` | `gregale event` |
| `gregale flags` | `gregale flag` |
| `gregale freeze` | `gregale release freeze` |
| `gregale incidents` | `gregale alert incident` |
| `gregale inspect` | `gregale app health --explain` |
| `gregale invitations` | `gregale org` |
| `gregale invocations` | `gregale invocation` |
| `gregale invoices` | `gregale billing invoices` |
| `gregale issues` | `gregale debug issue` |
| `gregale jobs` | `gregale job` |
| `gregale keys` | `gregale key` |
| `gregale log-drains` | `gregale logs drain` |
| `gregale metrics` | `gregale metric` |
| `gregale mfa` | `gregale account mfa` |
| `gregale mirror` | `gregale release mirror` |
| `gregale openapi` | `gregale api spec` |
| `gregale operations` | `gregale job` |
| `gregale orgs` | `gregale org` |
| `gregale overage-cap` | `gregale billing spend-cap` |
| `gregale park` | `gregale app park` |
| `gregale plan` | `gregale billing plan` |
| `gregale platform-tenants` | `gregale api tenant` |
| `gregale previews` | `gregale feature` |
| `gregale profile` | `gregale config profile` |
| `gregale projects` | `gregale project` |
| `gregale ps` | `gregale app instances` |
| `gregale release-gates` | `gregale release gate` |
| `gregale routes` | `gregale api route` |
| `gregale runs` | `gregale sandbox` |
| `gregale scan` | `gregale deploy --plan` |
| `gregale scheduled-releases` | `gregale release schedule` |
| `gregale secrets` | `gregale env` |
| `gregale send` | `gregale event send` |
| `gregale slo` | `gregale alert slo list --window` |
| `gregale slos` | `gregale alert slo` |
| `gregale synthetics` | `gregale alert check` |
| `gregale tail` | `gregale invocation watch` |
| `gregale throttle-suggestions` | `gregale edge rule suggest --rate-limits` |
| `gregale trace` | `gregale debug trace` |
| `gregale traffic` | `gregale release traffic` |
| `gregale triggers` | `gregale trigger` |
| `gregale trusted-publishers` | `gregale registry key` |
| `gregale upload-cache` | `gregale config upload-cache` |
| `gregale usage` | `gregale billing usage` |
| `gregale wake` | `gregale app wake` |
| `gregale wake-ahead` | `gregale app wake-ahead` |
| `gregale wake-timeline` | `gregale app wake-timeline` |
| `gregale webhooks` | `gregale webhook` |
| `gregale workers` | `gregale worker` |
| `gregale workflows` | `gregale workflow` |

## Earlier paths

| Earlier | Now |
|---|---|
| `gregale account billing-run schedule-delete` | `gregale account billing-run schedule delete` |
| `gregale account billing-run schedule-set` | `gregale account billing-run schedule set` |
| `gregale alerts rotate-secret` | `gregale alert secret rotate` |
| `gregale approvals approve` | `gregale release approve` |
| `gregale approvals reject` | `gregale release reject` |
| `gregale apps restore` | `gregale app restore` |
| `gregale apps rm` | `gregale app delete` |
| `gregale apps streaming-cap` | `gregale app streaming-cap` |
| `gregale automations event-recover` | `gregale automation event recover` |
| `gregale automations event-recovery-start` | `gregale automation event-recovery start` |
| `gregale automations failure-policy` | `gregale automation failure policy` |
| `gregale automations failure-recovery-review` | `gregale automation failure-recovery review` |
| `gregale automations failure-resume` | `gregale automation failure resume` |
| `gregale automations failures` | `gregale automation failure list` |
| `gregale automations publish-policy` | `gregale automation policy publish` |
| `gregale automations recovery-cancel` | `gregale automation recovery cancel` |
| `gregale automations recovery-health` | `gregale automation recovery health` |
| `gregale automations recovery-plan` | `gregale automation recovery run` |
| `gregale automations recovery-policy-evaluate` | `gregale automation recovery-policy evaluate` |
| `gregale automations recovery-response-apply` | `gregale automation recovery-response apply` |
| `gregale automations recovery-response-status` | `gregale automation recovery-response status` |
| `gregale automations recovery-start` | `gregale automation recovery create` |
| `gregale automations recovery-suppress` | `gregale automation recovery suppress` |
| `gregale bucket deletions start` | `gregale bucket deletion create` |
| `gregale bucket deletions status` | `gregale bucket deletion get` |
| `gregale bucket deletions` | `gregale bucket deletion` |
| `gregale bucket encryption` | `gregale bucket encryption set` |
| `gregale bucket object-lock` | `gregale bucket object-lock set` |
| `gregale bucket reconcile status` | `gregale bucket inventory get` |
| `gregale bucket reconcile` | `gregale bucket inventory` |
| `gregale bucket uploads status` | `gregale bucket multipart get` |
| `gregale bucket uploads` | `gregale bucket multipart` |
| `gregale bucket version-delete` | `gregale bucket version delete` |
| `gregale bucket versions` | `gregale bucket version` |
| `gregale chaos inject` | `gregale test chaos` |
| `gregale connect github` | `gregale github connect` |
| `gregale consumers billing-policy-delete` | `gregale api consumer billing-policy delete` |
| `gregale consumers billing-policy-set` | `gregale api consumer billing-policy set` |
| `gregale consumers billing-policy` | `gregale api consumer billing-policy get` |
| `gregale consumers credit-grant` | `gregale api consumer credit grant` |
| `gregale consumers credit-void` | `gregale api consumer credit void` |
| `gregale consumers key-create` | `gregale api consumer key create` |
| `gregale consumers key-revoke` | `gregale api consumer key revoke` |
| `gregale consumers overdue-holds` | `gregale api consumer overdue-hold list` |
| `gregale consumers plan-create` | `gregale api consumer plan create` |
| `gregale consumers plan-update` | `gregale api consumer plan update` |
| `gregale consumers plans` | `gregale api consumer plan list` |
| `gregale consumers rate-card-create` | `gregale api consumer rate-card create` |
| `gregale consumers rate-cards` | `gregale api consumer rate-card list` |
| `gregale consumers set-plan` | `gregale api consumer plan set` |
| `gregale consumers signup-delete` | `gregale api consumer signup delete` |
| `gregale consumers signup-set` | `gregale api consumer signup set` |
| `gregale consumers signup` | `gregale api consumer signup get` |
| `gregale consumers spend-cap-delete` | `gregale api consumer spend-cap delete` |
| `gregale consumers spend-cap-set` | `gregale api consumer spend-cap set` |
| `gregale consumers statement-draft` | `gregale api consumer statement draft` |
| `gregale consumers statement-export` | `gregale api consumer statement export` |
| `gregale consumers statement-finalize` | `gregale api consumer statement finalize` |
| `gregale consumers statement-handoff` | `gregale api consumer statement handoff` |
| `gregale consumers statement-show` | `gregale api consumer statement get` |
| `gregale consumers trial-end` | `gregale api consumer trial end` |
| `gregale consumers trial-set` | `gregale api consumer trial set` |
| `gregale consumers trial` | `gregale api consumer trial get` |
| `gregale context presets` | `gregale config preset` |
| `gregale customer-operations attention-summary` | `gregale operation attention summary` |
| `gregale customer-operations attention` | `gregale operation attention list` |
| `gregale customer-operations bindings` | `gregale operation binding generate` |
| `gregale customer-operations delivery-attempts` | `gregale operation delivery attempts` |
| `gregale customer-operations delivery` | `gregale operation delivery get` |
| `gregale customer-operations outcome-summary` | `gregale operation outcome summary` |
| `gregale customer-operations retry-delivery` | `gregale operation delivery retry` |
| `gregale debug rollout` | `gregale debug explain` |
| `gregale delayed-task add` | `gregale cron create --at` |
| `gregale delayed-task cancel` | `gregale cron cancel` |
| `gregale delayed-task get` | `gregale cron get --one-off` |
| `gregale delayed-task info` | `gregale cron get --one-off` |
| `gregale delayed-task list` | `gregale cron list --one-off` |
| `gregale deploy abort` | `gregale release attempt cancel` |
| `gregale deploy contract` | `gregale release contract run` |
| `gregale deploy recover` | `gregale release recover` |
| `gregale deploy resolve-contract` | `gregale release contract resolve` |
| `gregale deploy resume` | `gregale release resume` |
| `gregale deploy retry` | `gregale release attempt retry` |
| `gregale deploy status` | `gregale release attempt get` |
| `gregale deployment diff` | `gregale release diff` |
| `gregale deployment runtime` | `gregale release runtime` |
| `gregale deployment summary` | `gregale release summary` |
| `gregale deployment wait` | `gregale release wait` |
| `gregale deployments alias` | `gregale release alias` |
| `gregale deploys cancel` | `gregale release cancel` |
| `gregale deploys clear-obsolete` | `gregale release prune` |
| `gregale deploys clear` | `gregale release hide` |
| `gregale deploys logs` | `gregale release logs` |
| `gregale deploys reorder` | `gregale release reorder` |
| `gregale deploys retry` | `gregale release retry` |
| `gregale deploys show` | `gregale release stages` |
| `gregale deploys status` | `gregale release get` |
| `gregale domains set-default` | `gregale domain default set` |
| `gregale edge-rules waf-review` | `gregale edge rule waf review` |
| `gregale events backfill-items` | `gregale event backfill items` |
| `gregale events backfill-retry` | `gregale event backfill retry` |
| `gregale events backfill-status` | `gregale event backfill get` |
| `gregale events backfill` | `gregale event backfill create` |
| `gregale events deadline-alert-investigate` | `gregale event deadline-alert investigate` |
| `gregale events delivery-retry` | `gregale event delivery retry` |
| `gregale events notification-retry-apply` | `gregale event notification-retry apply` |
| `gregale events notification-retry-reconcile` | `gregale event notification-retry reconcile` |
| `gregale events recovery-cancel` | `gregale event recovery cancel` |
| `gregale events recovery-create` | `gregale event recovery create` |
| `gregale events recovery-health` | `gregale event recovery health` |
| `gregale events recovery-history` | `gregale event recovery history` |
| `gregale events recovery-items` | `gregale event recovery items` |
| `gregale events recovery-list` | `gregale event recovery list` |
| `gregale events recovery-notification-retry` | `gregale event recovery-notification retry` |
| `gregale events recovery-pause` | `gregale event recovery pause` |
| `gregale events recovery-rate` | `gregale event recovery update` |
| `gregale events recovery-resume` | `gregale event recovery resume` |
| `gregale events recovery-status` | `gregale event recovery get` |
| `gregale events subscription-capacity-command` | `gregale event subscription-capacity get` |
| `gregale events subscription-capacity-commands` | `gregale event subscription-capacity list` |
| `gregale events subscription-capacity` | `gregale event subscription-capacity set` |
| `gregale events subscription-circuit-disable` | `gregale event subscription-circuit disable` |
| `gregale events subscription-circuit-reset` | `gregale event subscription-circuit reset` |
| `gregale events subscription-circuit-set` | `gregale event subscription-circuit set` |
| `gregale events subscription-circuit-status` | `gregale event subscription-circuit get` |
| `gregale events subscription-health` | `gregale event subscription health` |
| `gregale events subscription-pause` | `gregale event subscription pause` |
| `gregale events subscription-resume` | `gregale event subscription resume` |
| `gregale events subscription-retry-reset` | `gregale event subscription-retry reset` |
| `gregale events subscription-retry-set` | `gregale event subscription-retry set` |
| `gregale events subscription-retry-status` | `gregale event subscription-retry get` |
| `gregale events subscription-status` | `gregale event subscription get` |
| `gregale events subscription-versions-reset` | `gregale event subscription-version reset` |
| `gregale events subscription-versions-set` | `gregale event subscription-version set` |
| `gregale events subscription-versions-status` | `gregale event subscription-version get` |
| `gregale events subscriptions` | `gregale event subscription list` |
| `gregale github bind` | `gregale github link` |
| `gregale invitations accept` | `gregale org invitation accept` |
| `gregale invitations peek` | `gregale org invitation peek` |
| `gregale issues compare-releases` | `gregale debug issue releases compare` |
| `gregale issues create-token` | `gregale debug issue token create` |
| `gregale issues revoke-token` | `gregale debug issue token revoke` |
| `gregale jobs artifact-download` | `gregale job artifact download` |
| `gregale mcp call` | `gregale mcp client call` |
| `gregale mcp complete` | `gregale mcp client complete` |
| `gregale mcp connections` | `gregale mcp connection` |
| `gregale mcp prompt-get` | `gregale mcp client render` |
| `gregale mcp prompts` | `gregale mcp client prompts` |
| `gregale mcp readiness maintenance cancel` | `gregale alert maintenance cancel` |
| `gregale mcp readiness maintenance create` | `gregale alert maintenance create` |
| `gregale mcp readiness maintenance list` | `gregale alert maintenance list` |
| `gregale mcp readiness pagerduty disable` | `gregale alert pagerduty disable` |
| `gregale mcp readiness pagerduty get` | `gregale alert pagerduty get` |
| `gregale mcp readiness pagerduty set` | `gregale alert pagerduty set` |
| `gregale mcp resource-read` | `gregale mcp client read` |
| `gregale mcp resource-watch` | `gregale mcp client watch` |
| `gregale mcp resources` | `gregale mcp client resources` |
| `gregale mcp task-cancel` | `gregale mcp client cancel` |
| `gregale mcp task-get` | `gregale mcp client status` |
| `gregale mcp task-wait` | `gregale mcp client wait` |
| `gregale mcp tools` | `gregale mcp client tools` |
| `gregale operations policy list` | `gregale job lock-policy list` |
| `gregale operations policy retire` | `gregale job lock-policy delete` |
| `gregale operations policy upsert` | `gregale job lock-policy set` |
| `gregale orgs invitations` | `gregale org invitation` |
| `gregale platform-tenants billing-policy-delete` | `gregale api tenant billing-policy delete` |
| `gregale platform-tenants billing-policy-set` | `gregale api tenant billing-policy set` |
| `gregale platform-tenants billing-policy` | `gregale api tenant billing-policy get` |
| `gregale platform-tenants credentials-apply` | `gregale api tenant credentials apply` |
| `gregale platform-tenants credentials-list` | `gregale api tenant credentials list` |
| `gregale platform-tenants credit-grant` | `gregale api tenant credit grant` |
| `gregale platform-tenants credit-void` | `gregale api tenant credit void` |
| `gregale platform-tenants link-consumer` | `gregale api tenant consumer link` |
| `gregale platform-tenants link-surface` | `gregale api tenant surface link` |
| `gregale platform-tenants overdue-holds` | `gregale api tenant overdue-hold list` |
| `gregale platform-tenants plan-create` | `gregale api tenant plan create` |
| `gregale platform-tenants plan-update` | `gregale api tenant plan update` |
| `gregale platform-tenants plans` | `gregale api tenant plan list` |
| `gregale platform-tenants rate-card-create` | `gregale api tenant rate-card create` |
| `gregale platform-tenants rate-cards` | `gregale api tenant rate-card list` |
| `gregale platform-tenants set-plan` | `gregale api tenant plan set` |
| `gregale platform-tenants spend-cap-delete` | `gregale api tenant spend-cap delete` |
| `gregale platform-tenants spend-cap-set` | `gregale api tenant spend-cap set` |
| `gregale platform-tenants statement-draft` | `gregale api tenant statement draft` |
| `gregale platform-tenants statement-export` | `gregale api tenant statement export` |
| `gregale platform-tenants statement-finalize` | `gregale api tenant statement finalize` |
| `gregale platform-tenants statement-handoff` | `gregale api tenant statement handoff` |
| `gregale platform-tenants statement-show` | `gregale api tenant statement get` |
| `gregale platform-tenants trial-end` | `gregale api tenant trial end` |
| `gregale platform-tenants trial-set` | `gregale api tenant trial set` |
| `gregale platform-tenants trial` | `gregale api tenant trial get` |
| `gregale previews join` | `gregale feature join` |
| `gregale previews leave` | `gregale feature leave` |
| `gregale projects environments recover-migration` | `gregale environment migration recover` |
| `gregale projects environments release-sets cancel-stage` | `gregale environment release-sets stage cancel` |
| `gregale projects environments release-sets prepare-deactivation` | `gregale environment release-sets deactivation prepare` |
| `gregale projects environments release-sets stage-status` | `gregale environment release-sets stage status` |
| `gregale projects environments` | `gregale environment` |
| `gregale queue tail` | `gregale queue watch` |
| `gregale registry published` | `gregale registry deploy` |
| `gregale secrets audit` | `gregale env audit` |
| `gregale secrets check` | `gregale env check` |
| `gregale secrets list` | `gregale env list` |
| `gregale secrets refs` | `gregale env ref` |
| `gregale secrets requirements` | `gregale env requirement` |
| `gregale secrets rotate` | `gregale env rotate` |
| `gregale secrets set` | `gregale env set` |
| `gregale secrets unset` | `gregale env unset` |
| `gregale webhooks account rotate-secret` | `gregale webhook account secret rotate` |
| `gregale webhooks rotate-secret` | `gregale webhook secret rotate` |

## Earlier flags

| Earlier | Now |
|---|---|
| `--environment`, `--scope`, `--stage` | `--env` |
| `--deployment`, `--deployment-id` | `--release` |
| `--after`, `--before` | `--cursor` |
| `--from`, `--start` | `--since` |
| `--to`, `--end` | `--until` |
| `--watch`, `--tail` | `--follow` |

A command keeps its earlier flag where the new spelling could mean something else, such as `--from` naming a source environment rather than a time.

<!-- END GENERATED -->
