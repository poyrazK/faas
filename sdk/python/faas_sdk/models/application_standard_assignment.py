from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.application_standard_assignment_scope import (
    ApplicationStandardAssignmentScope,
    check_application_standard_assignment_scope,
)

T = TypeVar("T", bound="ApplicationStandardAssignment")


@_attrs_define
class ApplicationStandardAssignment:
    id: UUID
    org_id: UUID
    scope: ApplicationStandardAssignmentScope
    scope_id: UUID
    standard_id: UUID
    admission_version: int
    revision: int
    active: bool
    created_by: UUID
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        org_id = str(self.org_id)

        scope: str = self.scope

        scope_id = str(self.scope_id)

        standard_id = str(self.standard_id)

        admission_version = self.admission_version

        revision = self.revision

        active = self.active

        created_by = str(self.created_by)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "org_id": org_id,
                "scope": scope,
                "scope_id": scope_id,
                "standard_id": standard_id,
                "admission_version": admission_version,
                "revision": revision,
                "active": active,
                "created_by": created_by,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        org_id = UUID(d.pop("org_id"))

        scope = check_application_standard_assignment_scope(d.pop("scope"))

        scope_id = UUID(d.pop("scope_id"))

        standard_id = UUID(d.pop("standard_id"))

        admission_version = d.pop("admission_version")

        revision = d.pop("revision")

        active = d.pop("active")

        created_by = UUID(d.pop("created_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        application_standard_assignment = cls(
            id=id,
            org_id=org_id,
            scope=scope,
            scope_id=scope_id,
            standard_id=standard_id,
            admission_version=admission_version,
            revision=revision,
            active=active,
            created_by=created_by,
            created_at=created_at,
            updated_at=updated_at,
        )

        application_standard_assignment.additional_properties = d
        return application_standard_assignment

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
