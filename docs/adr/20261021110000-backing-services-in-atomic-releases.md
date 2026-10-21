# ADR-20261021110000: Backing service declarations in atomic releases

## Status

Implemented. Amends ADR-20261019120004 (safe project source releases).

## Context

The repository scanner reports backing services declared beside a project's
workloads, for example a compose `db` service running a `postgres` image, as
managed declarations. Ordinary apply never provisions or configures them: they
appear in the plan with an environment hint and take part in dependency
validation, so `web` may depend on `db`. Real databases are bound through
`gregale add` or the manifest's `databases:` section, which are separate inputs.

The first safe release version refused any project with such a declaration.
Most compose projects declare one, so they could not use atomic releases at
all, although the declaration changes nothing an atomic release touches.

## Decision

Atomic releases, their readiness check and service additions accept backing
service declarations. The membership and configuration rules are unchanged,
and the workloads' dependencies, including those on backing services, must
form a valid graph, the same validation ordinary apply runs. Adding, removing
or changing a declaration does not change the release.

## Consequences

Compose projects with declared databases or caches can use `--atomic`. Manifest
database bindings and other managed bindings keep their existing delivery
contract and are not affected.

## Validation

`TestProjectSafeSourceAcceptsBackingServiceDeclarations` covers acceptance and
an invalid dependency on an undeclared service.
