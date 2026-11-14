# ADR-20261113090000: Company projects reach reviewed-plan, safe-release and source-ref deploys

Status: Accepted (2026-11-13)

## Context

Company projects deploy through `/v1/orgs/{organization}/projects/scan` and
`/deploy`, but three personal entry points had no company form, and the Go
client refused them under `WithProjectOrganization` (`gregale --org`):

- `POST /v1/projects/apply-plan`: apply the exact archive a scan reviewed;
- `POST /v1/projects/safe-deploy`: the atomic safe project release;
- `POST /v1/projects/scan/source-ref`: scan a connected repository at a ref.

The company scan also refused any `install_id`/`repo_full_name` binding
("repository ownership migration required"). That predates
ADR-20261110200000, which lets an adopted organization own GitHub
installations and binds company projects through them only (projects review
2026-11-12, follow-up 1).

## Decision

1. **Routes.** `POST /v1/orgs/{organization}/projects/apply-plan`,
   `/safe-deploy` and `/scan/source-ref` serve the same handlers as the
   personal forms. Each needs `deploy_project`. The client sends the personal
   methods there under `WithProjectOrganization`; nothing is personal-only.
2. **Repository resolution.** A company request resolves GitHub through the
   organization's namespace (`githubInstallOwner`): only an adopted, active
   organization owns installations, so a non-adopted organization answers 409
   and a member's personal installation answers 404, as for linking
   (ADR-20261110200000). The source stream runs as the organization's
   namespace. A company scan or deploy that binds `install_id` must name an
   installation the organization owns; the blanket refusal is removed.
3. **Plan tokens.** A plan token records the organization of a company scan
   (`org_id`). Applying it (multipart deploy or `apply-plan`) requires the same
   organization, so a personal plan never applies as a company deploy, or
   the reverse, even in a legacy organization that stores projects in the
   member's namespace.
4. **Attempt tracking.** The company deploy routes go through the same
   submission path as `POST /v1/projects`, so a `Deployment-Attempt-ID` is
   tracked for company deploys too.

## Consequences

- Plan tokens minted before this change for a company scan carry no
  organization and are refused once; scan again.
- Existing projects stored in a legacy creator namespace still use their
  project-specific scan and deploy URLs; the collection forms above follow the
  collection scan's rules for them.
