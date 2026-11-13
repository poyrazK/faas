# ADR-20261105104500: Project rollback undoes the latest deploy; dependency gates pin a serving release; pushes import sibling addresses

- Status: accepted
- Date: 2026-11-05
- Amends: ADR-20261103172200 (project rollback), ADR-20261102192943
  (Compose `service_healthy`), ADR-20261104221217 and ADR-20261023085230
  (Compose environment import)

## Context

`gregale project rollback` rolled every live service back to its previous
release, in deploy order. Three things went wrong:

- A service the latest deploy had not changed went back too, to whatever it
  ran before, possibly weeks earlier.
- A service the latest deploy added has no earlier release, so the command
  stopped there. When that service was a dependency, it stopped first and
  rolled nothing back.
- A rollback restores the release that served most recently. Running the
  command again after a partial failure therefore rolled the services it had
  already rolled back forward again.

Deploy order also rolled an API back before the callers that needed its new
version.

A `depends_on: condition: service_healthy` gate pinned the dependency's
newest deployment by revision, whatever its status. A rollback reuses the
older release's row instead of creating one, so after a dependency was rolled
back, or after its last deploy failed, the newest revision was a release that
would never serve again. Every later deploy of a service waiting for it
failed at once with "dependency … is unavailable", although the dependency
was serving.

ADR-20261104221217 made sibling service addresses import without the
`x-gregale-env-import` opt-in. The plan listed them as imported, but the
apply path, and the PR preview path, returned before importing anything when
the project had not opted in. A GitHub push imported nothing at all, so with
the opt-in a service a PR added worked in its preview and then started in
production with its sibling's URL unset.

## Decision

1. `gregale project rollback` rolls back the project's latest deploy. That
   deploy is the newest release any service serves; a service belongs to it
   when it serves the same commit or, for releases without a commit, one
   created within 15 minutes of it. `--all` rolls back every service. Services
   added since (no rollback target) and services already rolled back (target
   newer than the serving release) are skipped with the reason. Callers roll
   back before the services they depend on. Each step pins
   `expected_current_deployment_id` and `target_deployment_id` to the plan,
   so a concurrent deploy fails the step instead of being rolled back. The
   dry run prints each service's revisions and every skip.
2. A `service_healthy` gate pins the dependency's newest release that is not
   failed, superseded or cancelled: the release deployed with it, or the one
   serving. A dependency with no such release refuses the admission as before.
3. Sibling service addresses import on every path. The project apply and the
   PR preview no longer return early without the opt-in. A GitHub push sends
   each service's addresses to apid on `EnqueueBuildRequest.compose_service_addresses`
   (field 19, additive). apid drops any value that is not a Gregale service
   address, writes the keys the deployment's scope has no variable or secret
   for, and refuses the build only when they would exceed the plan's variable
   limit. A push imports no other value.

## Consequences

- An older githubd does not send the addresses, and an older apid ignores
  them; either way the push behaves as before.
- Grouping a deploy by commit or creation time is a heuristic. Two pushes
  within 15 minutes without commits are one deploy to the rollback; the dry
  run shows the grouping, and `--all` overrides it.
- A rerun after a failed step is safe only while nobody deploys in between; a
  deploy in between makes that service's newest release the latest deploy
  again.

## Alternatives rejected

- Recording each project deploy's members on the server: the right long-term
  model, but it needs a table, an API field and SDK changes. The CLI change
  fixes the observed failures now.
- Rolling back in deploy order with a compatibility warning: callers would
  still call an API that had already gone back.
- Writing the addresses from githubd: apid is the only writer of an app's
  configuration.
