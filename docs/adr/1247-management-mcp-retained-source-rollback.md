# ADR-1247: Retained GitHub source rollback through management MCP

## Status

Accepted. Extends ADR-1244 and ADR-1246.

## Context

Native workload rollback accepts retained images, while source deployments need
a new build of a historical commit. Reusing HTTP traffic weights cannot roll
back worker consumers. Restoring previous configuration without review would
also override current customer intent.

## Decision

Allow the existing workload planning tool to select a retained live or
superseded GitHub deployment in the exact app and scope. Require canonical
GitHub repository/commit provenance, an exact matching commit, and an unchanged
build root. Reject uploads, arbitrary URLs, incomplete provenance and changed
scope or identity. Review the historical commit against current GitHub access
and current workload intent, then reuse the existing reviewed native source
apply path, quotas, protected-environment gates and encrypted retry journal.

The plan identifies the retained deployment and reviewed source, while apply
creates a new build and deployment generation under current settings. Service
handoff and worker selection remain native controller responsibilities.

## Consequences

This is a source rebuild, not restoration of an identical artifact or old
configuration. Mutable build dependencies can change output. Current repository
and installation access are required; historical installation identity is not
recovered from the deployment URL. Repository configuration declarations are
not imported. External queue effects are not reversed. Native qualification must
check the new artifact, handoff and queue behavior separately from local tests.
