from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_migration_build_wait import ProjectMigrationBuildWait
    from ..models.project_migration_service_public_build_urls import ProjectMigrationServicePublicBuildUrls
    from ..models.project_migration_service_public_service_connections import (
        ProjectMigrationServicePublicServiceConnections,
    )
    from ..models.project_migration_service_service_connections import ProjectMigrationServiceServiceConnections


T = TypeVar("T", bound="ProjectMigrationService")


@_attrs_define
class ProjectMigrationService:
    """Progress and historical evidence pointers for one migration service and its current attempt."""

    name: str
    """Selected workload name."""
    attempt: int
    status: str
    """Observed state of the reserved deployment, waiting for build APIs, pending work or changed application."""
    depends_on: list[str]
    required_env_keys: list[str]
    configured_keys: list[str]
    build_dependencies: list[str] | Unset = UNSET
    """Saved declared API services required before this migration service builds."""
    build_wait: ProjectMigrationBuildWait | Unset = UNSET
    """Durable readiness progress before a migration build consumes a builder slot. Public URLs still route
    normally; this is a point-in-time readiness check."""
    public_service_connections: ProjectMigrationServicePublicServiceConnections | Unset = UNSET
    """Saved public build variable-to-service choices."""
    public_build_urls: ProjectMigrationServicePublicBuildUrls | Unset = UNSET
    """Explicitly reviewed public HTTPS URLs. Saved values may appear in browser bundles; credentials, queries and
    fragments are rejected."""
    app: str | Unset = UNSET
    """Current application slug."""
    app_id: UUID | Unset = UNSET
    """Application identity bound before enqueue."""
    deployment_id: UUID | Unset = UNSET
    """Exact reserved deployment for this attempt."""
    build_id: UUID | Unset = UNSET
    """Exact reserved build for this attempt."""
    reason: str | Unset = UNSET
    """Metadata-only explanation for unfinished work."""
    service_connections: ProjectMigrationServiceServiceConnections | Unset = UNSET
    """Saved runtime variable names mapped to selected declared services. Resumed automatically; contains no
    values."""
    verification_id: UUID | Unset = UNSET
    """Latest recorded verification identity for this exact deployment."""
    verification_status: str | Unset = UNSET
    """Last recorded verification result; reopen verification to validate current configuration."""
    cutover_id: UUID | Unset = UNSET
    """Latest saved cutover identity for this exact deployment."""
    cutover_status: str | Unset = UNSET
    """Last recorded cutover state; reopen cutover to validate current configuration."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        attempt = self.attempt

        status = self.status

        depends_on = self.depends_on

        required_env_keys = self.required_env_keys

        configured_keys = self.configured_keys

        build_dependencies: list[str] | Unset = UNSET
        if not isinstance(self.build_dependencies, Unset):
            build_dependencies = self.build_dependencies

        build_wait: dict[str, Any] | Unset = UNSET
        if not isinstance(self.build_wait, Unset):
            build_wait = self.build_wait.to_dict()

        public_service_connections: dict[str, Any] | Unset = UNSET
        if not isinstance(self.public_service_connections, Unset):
            public_service_connections = self.public_service_connections.to_dict()

        public_build_urls: dict[str, Any] | Unset = UNSET
        if not isinstance(self.public_build_urls, Unset):
            public_build_urls = self.public_build_urls.to_dict()

        app = self.app

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        build_id: str | Unset = UNSET
        if not isinstance(self.build_id, Unset):
            build_id = str(self.build_id)

        reason = self.reason

        service_connections: dict[str, Any] | Unset = UNSET
        if not isinstance(self.service_connections, Unset):
            service_connections = self.service_connections.to_dict()

        verification_id: str | Unset = UNSET
        if not isinstance(self.verification_id, Unset):
            verification_id = str(self.verification_id)

        verification_status = self.verification_status

        cutover_id: str | Unset = UNSET
        if not isinstance(self.cutover_id, Unset):
            cutover_id = str(self.cutover_id)

        cutover_status = self.cutover_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "attempt": attempt,
                "status": status,
                "depends_on": depends_on,
                "required_env_keys": required_env_keys,
                "configured_keys": configured_keys,
            }
        )
        if build_dependencies is not UNSET:
            field_dict["build_dependencies"] = build_dependencies
        if build_wait is not UNSET:
            field_dict["build_wait"] = build_wait
        if public_service_connections is not UNSET:
            field_dict["public_service_connections"] = public_service_connections
        if public_build_urls is not UNSET:
            field_dict["public_build_urls"] = public_build_urls
        if app is not UNSET:
            field_dict["app"] = app
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if build_id is not UNSET:
            field_dict["build_id"] = build_id
        if reason is not UNSET:
            field_dict["reason"] = reason
        if service_connections is not UNSET:
            field_dict["service_connections"] = service_connections
        if verification_id is not UNSET:
            field_dict["verification_id"] = verification_id
        if verification_status is not UNSET:
            field_dict["verification_status"] = verification_status
        if cutover_id is not UNSET:
            field_dict["cutover_id"] = cutover_id
        if cutover_status is not UNSET:
            field_dict["cutover_status"] = cutover_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_migration_build_wait import ProjectMigrationBuildWait
        from ..models.project_migration_service_public_build_urls import ProjectMigrationServicePublicBuildUrls
        from ..models.project_migration_service_public_service_connections import (
            ProjectMigrationServicePublicServiceConnections,
        )
        from ..models.project_migration_service_service_connections import ProjectMigrationServiceServiceConnections

        d = dict(src_dict)
        name = d.pop("name")

        attempt = d.pop("attempt")

        status = d.pop("status")

        depends_on = cast(list[str], d.pop("depends_on"))

        required_env_keys = cast(list[str], d.pop("required_env_keys"))

        configured_keys = cast(list[str], d.pop("configured_keys"))

        build_dependencies = cast(list[str], d.pop("build_dependencies", UNSET))

        _build_wait = d.pop("build_wait", UNSET)
        build_wait: ProjectMigrationBuildWait | Unset
        if isinstance(_build_wait, Unset):
            build_wait = UNSET
        else:
            build_wait = ProjectMigrationBuildWait.from_dict(_build_wait)

        _public_service_connections = d.pop("public_service_connections", UNSET)
        public_service_connections: ProjectMigrationServicePublicServiceConnections | Unset
        if isinstance(_public_service_connections, Unset):
            public_service_connections = UNSET
        else:
            public_service_connections = ProjectMigrationServicePublicServiceConnections.from_dict(
                _public_service_connections
            )

        _public_build_urls = d.pop("public_build_urls", UNSET)
        public_build_urls: ProjectMigrationServicePublicBuildUrls | Unset
        if isinstance(_public_build_urls, Unset):
            public_build_urls = UNSET
        else:
            public_build_urls = ProjectMigrationServicePublicBuildUrls.from_dict(_public_build_urls)

        app = d.pop("app", UNSET)

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        _build_id = d.pop("build_id", UNSET)
        build_id: UUID | Unset
        if isinstance(_build_id, Unset):
            build_id = UNSET
        else:
            build_id = UUID(_build_id)

        reason = d.pop("reason", UNSET)

        _service_connections = d.pop("service_connections", UNSET)
        service_connections: ProjectMigrationServiceServiceConnections | Unset
        if isinstance(_service_connections, Unset):
            service_connections = UNSET
        else:
            service_connections = ProjectMigrationServiceServiceConnections.from_dict(_service_connections)

        _verification_id = d.pop("verification_id", UNSET)
        verification_id: UUID | Unset
        if isinstance(_verification_id, Unset):
            verification_id = UNSET
        else:
            verification_id = UUID(_verification_id)

        verification_status = d.pop("verification_status", UNSET)

        _cutover_id = d.pop("cutover_id", UNSET)
        cutover_id: UUID | Unset
        if isinstance(_cutover_id, Unset):
            cutover_id = UNSET
        else:
            cutover_id = UUID(_cutover_id)

        cutover_status = d.pop("cutover_status", UNSET)

        project_migration_service = cls(
            name=name,
            attempt=attempt,
            status=status,
            depends_on=depends_on,
            required_env_keys=required_env_keys,
            configured_keys=configured_keys,
            build_dependencies=build_dependencies,
            build_wait=build_wait,
            public_service_connections=public_service_connections,
            public_build_urls=public_build_urls,
            app=app,
            app_id=app_id,
            deployment_id=deployment_id,
            build_id=build_id,
            reason=reason,
            service_connections=service_connections,
            verification_id=verification_id,
            verification_status=verification_status,
            cutover_id=cutover_id,
            cutover_status=cutover_status,
        )

        project_migration_service.additional_properties = d
        return project_migration_service

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
