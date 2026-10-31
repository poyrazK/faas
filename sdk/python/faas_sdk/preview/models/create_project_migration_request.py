from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_project_migration_request_build_dependencies import (
        CreateProjectMigrationRequestBuildDependencies,
    )
    from ..models.project_initial_secrets import ProjectInitialSecrets
    from ..models.project_public_build_ur_ls import ProjectPublicBuildURLs
    from ..models.project_service_connections import ProjectServiceConnections


T = TypeVar("T", bound="CreateProjectMigrationRequest")


@_attrs_define
class CreateProjectMigrationRequest:
    """Start one durable migration of selected reviewed services with request-only configuration."""

    id: UUID
    """Client-generated identity saved before submission."""
    plan_token: str
    """Reviewed migration-mode source token; never persisted in the receipt."""
    only: list[str]
    initial_secrets: ProjectInitialSecrets | Unset = UNSET
    """Request-only mapping of exact selected workload names to initial sealed environment values. Values stay in
    memory until sealing and never enter responses or source caches."""
    service_connections: ProjectServiceConnections | Unset = UNSET
    """Selected caller names mapped to variable-to-service choices. Targets must be selected declared HTTP or
    GraphQL dependencies. service_connections uses private runtime URLs; public_service_connections uses reviewed
    public build URLs."""
    public_service_connections: ProjectServiceConnections | Unset = UNSET
    """Selected caller names mapped to variable-to-service choices. Targets must be selected declared HTTP or
    GraphQL dependencies. service_connections uses private runtime URLs; public_service_connections uses reviewed
    public build URLs."""
    public_build_urls: ProjectPublicBuildURLs | Unset = UNSET
    """Explicit public HTTPS build URLs for selected services, persisted and exposed in frontend bundles. Keys use
    VITE_, NEXT_PUBLIC_, REACT_APP_, PUBLIC_, NUXT_PUBLIC_ or GATSBY_ and end in _URL. URLs may include a base path
    but no credentials, query, fragment, IP, localhost or private service name. Up to ten URLs per service. Omitted
    keys keep their existing runtime configuration."""
    build_dependencies: CreateProjectMigrationRequestBuildDependencies | Unset = UNSET
    """Optional caller-to-API build waits. Targets must be selected declared dependencies. Each attempt pins the
    saved API deployment, waits up to 30 minutes without using a builder slot, and requires it to be live with 100
    percent traffic. Resume restores these choices."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        plan_token = self.plan_token

        only = self.only

        initial_secrets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.initial_secrets, Unset):
            initial_secrets = self.initial_secrets.to_dict()

        service_connections: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_connections, Unset):
            service_connections = self.service_connections.to_dict()

        public_service_connections: dict[str, Any] | Unset = UNSET
        if not isinstance(self.public_service_connections, Unset):
            public_service_connections = self.public_service_connections.to_dict()

        public_build_urls: dict[str, Any] | Unset = UNSET
        if not isinstance(self.public_build_urls, Unset):
            public_build_urls = self.public_build_urls.to_dict()

        build_dependencies: dict[str, Any] | Unset = UNSET
        if not isinstance(self.build_dependencies, Unset):
            build_dependencies = self.build_dependencies.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "plan_token": plan_token,
                "only": only,
            }
        )
        if initial_secrets is not UNSET:
            field_dict["initial_secrets"] = initial_secrets
        if service_connections is not UNSET:
            field_dict["service_connections"] = service_connections
        if public_service_connections is not UNSET:
            field_dict["public_service_connections"] = public_service_connections
        if public_build_urls is not UNSET:
            field_dict["public_build_urls"] = public_build_urls
        if build_dependencies is not UNSET:
            field_dict["build_dependencies"] = build_dependencies

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_project_migration_request_build_dependencies import (
            CreateProjectMigrationRequestBuildDependencies,
        )
        from ..models.project_initial_secrets import ProjectInitialSecrets
        from ..models.project_public_build_ur_ls import ProjectPublicBuildURLs
        from ..models.project_service_connections import ProjectServiceConnections

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        plan_token = d.pop("plan_token")

        only = cast(list[str], d.pop("only"))

        _initial_secrets = d.pop("initial_secrets", UNSET)
        initial_secrets: ProjectInitialSecrets | Unset
        if isinstance(_initial_secrets, Unset):
            initial_secrets = UNSET
        else:
            initial_secrets = ProjectInitialSecrets.from_dict(_initial_secrets)

        _service_connections = d.pop("service_connections", UNSET)
        service_connections: ProjectServiceConnections | Unset
        if isinstance(_service_connections, Unset):
            service_connections = UNSET
        else:
            service_connections = ProjectServiceConnections.from_dict(_service_connections)

        _public_service_connections = d.pop("public_service_connections", UNSET)
        public_service_connections: ProjectServiceConnections | Unset
        if isinstance(_public_service_connections, Unset):
            public_service_connections = UNSET
        else:
            public_service_connections = ProjectServiceConnections.from_dict(_public_service_connections)

        _public_build_urls = d.pop("public_build_urls", UNSET)
        public_build_urls: ProjectPublicBuildURLs | Unset
        if isinstance(_public_build_urls, Unset):
            public_build_urls = UNSET
        else:
            public_build_urls = ProjectPublicBuildURLs.from_dict(_public_build_urls)

        _build_dependencies = d.pop("build_dependencies", UNSET)
        build_dependencies: CreateProjectMigrationRequestBuildDependencies | Unset
        if isinstance(_build_dependencies, Unset):
            build_dependencies = UNSET
        else:
            build_dependencies = CreateProjectMigrationRequestBuildDependencies.from_dict(_build_dependencies)

        create_project_migration_request = cls(
            id=id,
            plan_token=plan_token,
            only=only,
            initial_secrets=initial_secrets,
            service_connections=service_connections,
            public_service_connections=public_service_connections,
            public_build_urls=public_build_urls,
            build_dependencies=build_dependencies,
        )

        return create_project_migration_request
