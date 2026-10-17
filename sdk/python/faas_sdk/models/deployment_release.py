from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_release_status import DeploymentReleaseStatus, check_deployment_release_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_database_ref import ReleaseDatabaseRef


T = TypeVar("T", bound="DeploymentRelease")


@_attrs_define
class DeploymentRelease:
    """A deployment's release command run (ADR-1448)."""

    status: DeploymentReleaseStatus
    """The release task's status; pending before imaged has admitted the task."""
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    migration_databases: list[ReleaseDatabaseRef] | Unset = UNSET
    """Managed PostgreSQL databases whose migration binding the release task receives."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        migration_databases: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.migration_databases, Unset):
            migration_databases = []
            for migration_databases_item_data in self.migration_databases:
                migration_databases_item = migration_databases_item_data.to_dict()
                migration_databases.append(migration_databases_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if migration_databases is not UNSET:
            field_dict["migration_databases"] = migration_databases

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_database_ref import ReleaseDatabaseRef

        d = dict(src_dict)
        status = check_deployment_release_status(d.pop("status"))

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        _migration_databases = d.pop("migration_databases", UNSET)
        migration_databases: list[ReleaseDatabaseRef] | Unset = UNSET
        if _migration_databases is not UNSET:
            migration_databases = []
            for migration_databases_item_data in _migration_databases:
                migration_databases_item = ReleaseDatabaseRef.from_dict(migration_databases_item_data)

                migration_databases.append(migration_databases_item)

        deployment_release = cls(
            status=status,
            started_at=started_at,
            finished_at=finished_at,
            migration_databases=migration_databases,
        )

        deployment_release.additional_properties = d
        return deployment_release

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
