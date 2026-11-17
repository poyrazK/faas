# Support access: deploying, rolling back and restarting for a customer

Use this runbook when a customer asks Gregale support to deploy, retry, roll
back or restart one of their apps, for example because their CLI or CI cannot
deploy and they cannot tell why. Support never signs in as the customer and
never asks for their keys or passwords. The customer gives support a
time-limited **support grant**, and support acts from its own operator session.

Decision: ADR-20261116163508. Related: [org-owner-recovery.md](org-owner-recovery.md).

The feature is released per organization by the `support_grants` platform
flag. While the flag is off for an organization, it cannot grant access and
support cannot request it.

## What a grant allows

Under a grant, support can:

- deploy the app's connected GitHub repository at its **production branch**;
- retry a deployment from a stage, or cancel one;
- roll back, optionally to a chosen earlier deployment;
- restart, optionally with `--fresh`;
- read the app, its deployments, stages, timeline and build logs.

Everything else is refused, whatever the operator's own rights: secrets and
environment values, configuration, domains, edge rules, members, keys, billing,
deleting anything, runtime logs and request data. Uploaded source and a commit
SHA or other ref cannot be deployed. Extra request fields are refused, so
support cannot turn off health checks, canaries, freezes or rollback on 5xx,
pick another environment, or restore databases on rollback.

If the customer's problem needs one of those, it is the customer's to do. Tell
them exactly what to change.

## 1. Sign in and check for a grant

```sh
gregalectl auth login --email <your operator email>
gregalectl support-access grants
```

The list shows every live grant: its id, organization, apps, end time, who
granted it and why. If the customer already granted access, go to step 3.

## 2. Ask for access

```sh
gregalectl support-access request --org <org slug> --reason "<why, in words the customer understands>"
```

The customer reads the reason. Say what you will do and why, for example
"Your last three deploys failed at the build stage; we would like to retry the
build and, if it fails again, roll back to deployment 41." Do not ask for more
apps or a longer time than the work needs.

The organization's owners and admins (for a personal account, its owner) get an
email and approve on the dashboard's Support access page, choosing the apps and
how long (24 hours by default, 7 days at most). A request expires after 24
hours; an organization has at most 3 open requests.

Never ask the customer to invite you as a member or to send a key instead. That
gives far more access, with no end date and no support label in their logs.

## 3. Act under the grant

Writes need a step-up within the last 5 minutes (`gregalectl auth step-up`).

```sh
G=<grant id>
gregalectl support-access status   --grant $G --app <slug>
gregalectl support-access deploy   --grant $G --app <slug> --repo <owner/name> --ref <production branch> --reason "<why>"
gregalectl support-access retry    --grant $G --deployment <id> [--from-stage build]
gregalectl support-access cancel   --grant $G --app <slug> --deployment <id>
gregalectl support-access rollback --grant $G --app <slug> [--to <deployment id>] --reason "<why>"
gregalectl support-access restart  --grant $G --app <slug> [--fresh]
```

`deploy` takes the app's connected repository and its production branch only;
anything else is refused with 403. If the app has no connected repository,
support cannot deploy it: use retry or rollback, or ask the customer to deploy.

Every request is recorded in the customer's activity log as "Gregale support
(<you>)", and owners and admins are emailed when support changes something (at
most once every 10 minutes). Write each action and its result in the ticket too.

## 4. Finish

Tell the customer what you did and the result. Ask them to revoke the grant
when the issue is solved; it also ends on its own at its end time, and when the
person who granted it stops being an owner or admin.

## Incident: support access misused or an operator session stolen

1. Turn the `support_grants` platform flag off. Its kill switch refuses every
   grant at once, including writes in flight:
   `gregalectl flags disable --key support_grants --reason "<incident>" --yes`.
2. Revoke the operator's sessions:
   `gregalectl accounts revoke-sessions --account-id <operator uuid> --reason support_access_incident --yes`.
3. List the live grants (`gregalectl support-access grants`) and, for each
   organization, what the operator did: `support.action` audit events and
   `support_access.*` rows in the organization's activity log
   (`gregalectl accounts activity --account-id <owner uuid>`). Tell each affected
   customer, following [breach-notification.md](breach-notification.md) if
   customer data may have been exposed.

Customers can revoke at any time themselves: on the dashboard's Support access
page, or with `gregale org support revoke --org <slug> <grant id>`.
