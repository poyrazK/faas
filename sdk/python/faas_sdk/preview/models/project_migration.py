from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_migration_status import ProjectMigrationStatus, check_project_migration_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_migration_service import ProjectMigrationService


T = TypeVar("T", bound="ProjectMigration")


@_attrs_define
class ProjectMigration:
    """Creator-owned migration receipt linking selected services to exact deployment identities."""

    version: int
    """Monotonic journal version required to resume the reviewed set of services."""
    id: UUID
    """Durable project migration identity."""
    project: str
    """Creator-owned project slug."""
    source_sha256: str
    """Checksum of the original reviewed source archive."""
    status: ProjectMigrationStatus
    services: list[ProjectMigrationService]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    project_id: UUID | Unset = UNSET
    """Project identity after reconciliation."""
    reason: str | Unset = UNSET
    """Project migration recovery guidance."""
    retry_after: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        id = str(self.id)

        project = self.project

        source_sha256 = self.source_sha256

        status: str = self.status

        services = []
        for services_item_data in self.services:
            services_item = services_item_data.to_dict()
            services.append(services_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        reason = self.reason

        retry_after: str | Unset = UNSET
        if not isinstance(self.retry_after, Unset):
            retry_after = self.retry_after.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "id": id,
                "project": project,
                "source_sha256": source_sha256,
                "status": status,
                "services": services,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if reason is not UNSET:
            field_dict["reason"] = reason
        if retry_after is not UNSET:
            field_dict["retry_after"] = retry_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_migration_service import ProjectMigrationService

        d = dict(src_dict)
        version = d.pop("version")

        id = UUID(d.pop("id"))

        project = d.pop("project")

        source_sha256 = d.pop("source_sha256")

        status = check_project_migration_status(d.pop("status"))

        services = []
        _services = d.pop("services")
        for services_item_data in _services:
            services_item = ProjectMigrationService.from_dict(services_item_data)

            services.append(services_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        reason = d.pop("reason", UNSET)

        _retry_after = d.pop("retry_after", UNSET)
        retry_after: datetime.datetime | Unset
        if isinstance(_retry_after, Unset):
            retry_after = UNSET
        else:
            retry_after = datetime.datetime.fromisoformat(_retry_after)

        project_migration = cls(
            version=version,
            id=id,
            project=project,
            source_sha256=source_sha256,
            status=status,
            services=services,
            created_at=created_at,
            updated_at=updated_at,
            project_id=project_id,
            reason=reason,
            retry_after=retry_after,
        )

        project_migration.additional_properties = d
        return project_migration

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
