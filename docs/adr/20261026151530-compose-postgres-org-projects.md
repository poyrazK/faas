# ADR-20261026151530: Organization-owned projects get Compose postgres as an organization database

- **Status:** accepted
- **Date:** 2026-10-26
- **Amends:** ADR-20261025111824 (a Compose `postgres` service becomes a managed database), which left organization-owned projects to attach databases by hand
- **Decision:** In an organization-owned project, a Compose `postgres` service becomes an organization database under the same rules as in a personal project, provided the caller may create and bind organization databases.
  - **Ownership.** The project's access path already scopes managed PostgreSQL to the organization, so the database belongs to it. That is the same ownership `gregale postgres create` and a manifest binding use in that project.
  - **Permission.** The caller needs the `org.manage_postgres` and `org.bind_postgres` actions for their role, and a key with `postgres:manage` (or `admin`). These are the permissions that creating a database and binding one from a manifest already require.
    - **Plan:** asks without recording a denial, since a plan is a dry run. Without permission, the plan lists the service with the new reason `permission`.
    - **Apply:** asks again with audit, so a denial is recorded as for any organization action.
  - Everything else is unchanged: the same version, quota, reuse and binding rules.
- **Why:** Organization projects are where teams deploy shared services, and they were the one case where a Compose `postgres` still meant creating and binding a database by hand. The organization ownership and permission checks it needs already existed for the manifest path.
- **Consequences:**
  - A deploy-only key, or a role without database permissions, gets the `permission` reason instead of a database. The plan shows it, and apply never creates a database the caller could not create directly.
  - The database counts toward the organization's databases and its plan's limits.
  - Rolling upgrades: `permission` is a new value of `not_provisioned_reason`. An older CLI prints the plan without a reason text for it.
- **Rejected alternatives:**
  - **Provide it whatever the caller's role.** A deploy should not create an organization resource the deployer could not create directly.
  - **Audit denials in the plan.** A plan would then record a denial each time a reviewer looks at it.
