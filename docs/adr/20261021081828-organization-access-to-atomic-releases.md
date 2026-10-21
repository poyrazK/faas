# ADR-20261021081828: Organization access to atomic releases

## Status

Implemented. Extends ADR-202610171905 (organization project ownership) to the
release operations of ADR-20261019120000 through ADR-20261019120055. Release
admission flags are unchanged and remain off by default.

## Context

Project routes are authorized per reviewed route (`projectaccess/routes.go`).
The atomic release work added routes for safe deploys, submission receipts and
controls, release secrets, readiness, secret-aware promotion previews,
promotion controls and promotion migration recovery, but none was reviewed for
organizations. A company project therefore refused all of them with
`org_role_forbidden`, and the company deploy URLs bypassed durable submission
receipts because they did not go through the submission wrapper.

Two further gaps made the release state unusable for adopted organizations:

- The release tables referenced `accounts(id)`. An adopted organization keeps
  its projects in its own resource namespace, which is not an account, so
  receipts, worker pins, canaries, secret bundles, service additions and
  migration contracts could not be written.
- The slug-less receipt URL `/v1/project-submissions/{attempt}` resolves the
  caller's own namespace without a project check. For a legacy company project
  in a creator's namespace, a departed creator kept read and control access to
  its releases.

## Decision

Company projects get the release operations under
`/v1/orgs/{organization}/projects/{slug}` with these roles:

| Operation | Action | Roles |
|---|---|---|
| Readiness, secret-aware promotion preview, receipt reads | `org.read_project` | owner, admin, developer, viewer |
| Release secret staging | `org.configure_project` | owner, admin, developer (as app secrets) |
| Safe deploy, release controls, promotion abort/recover, migration recovery | `org.deploy_project` | owner, admin, developer |

- Safe deploys are addressed by project (`/{slug}/safe-deploy`). A slug-less
  company form would resolve the uploaded slug in a namespace that can also
  hold a member's personal projects; the existing scan binding requires the
  uploaded `project_slug` to match the path.
- Company deploy URLs use the submission wrapper, so `Deployment-Attempt-ID`
  records a receipt exactly as on the personal URL.
- Receipts are read and controlled at `/{slug}/submissions/{attempt}`. The
  receipt must name the addressed project; another project's attempt is not
  found. The personal receipt URL no longer returns receipts of company
  projects, so access follows current membership and role.
- Readiness may name a project the company does not have yet, like the
  personal pre-creation check, and authorizes like the company collection. A
  slug already used by a project outside the company returns 409.
- The app routes for deployment receipts and policy staging are reviewed
  (`org.read_app`, and `org.configure_app` for staging, which carries only the
  profiling/tracing settings `PATCH /v1/apps/{slug}` accepts). Cron staging
  stays unreviewed, as app crons are.
- The six release tables reference `resource_namespaces(id)`, replaced online
  in three steps. MemStore accepts any resource namespace, and the service
  addition and cron activation plan lookups use the adopted organization's plan
  for a company namespace.

## Consequences

Teams can use atomic releases, promotion controls and receipts on company
projects with the same role boundaries as existing deploy and promotion
routes. Go clients gain `*Org*` methods; `DeployOrgProject` honors the
safe-release context and attempt identity. Generated Node and Python clients
include the new operations. The CLI still addresses only personal project URLs.

The receipt migration is a contract migration and is applied by a full
release. Until then, company namespaces continue to fail receipt writes.

## Validation

- `TestOrgProjectReleaseOperationsHTTP` and `TestOrgProjectDeploySubmissionHTTP`
  cover role outcomes, receipt binding, the hidden personal receipt URL, company
  pre-creation readiness and slug binding for company safe deploys.
- `TestReleaseOperationActions` pins the reviewed actions.
- `TestProjectSubmissionCompanyNamespace{Memory,Postgres}` records a receipt
  under an adopted organization's namespace.
