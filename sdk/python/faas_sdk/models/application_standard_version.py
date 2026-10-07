from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.application_standard_definition import ApplicationStandardDefinition


T = TypeVar("T", bound="ApplicationStandardVersion")


@_attrs_define
class ApplicationStandardVersion:
    """One immutable canonical standard version and publishing provenance."""

    standard_id: UUID
    org_id: UUID
    slug: str
    version: int
    definition: ApplicationStandardDefinition
    """Supported versioned application requirements. Resource UUIDs must belong to the organization.
    Enforced destination, publisher and CIDR sets cannot be empty.
    Empty local CIDRs mean unrestricted access and cannot satisfy a restriction.
    """
    definition_hash: str
    description: str
    created_by: UUID
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        standard_id = str(self.standard_id)

        org_id = str(self.org_id)

        slug = self.slug

        version = self.version

        definition = self.definition.to_dict()

        definition_hash = self.definition_hash

        description = self.description

        created_by = str(self.created_by)

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "standard_id": standard_id,
                "org_id": org_id,
                "slug": slug,
                "version": version,
                "definition": definition,
                "definition_hash": definition_hash,
                "description": description,
                "created_by": created_by,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_definition import ApplicationStandardDefinition

        d = dict(src_dict)
        standard_id = UUID(d.pop("standard_id"))

        org_id = UUID(d.pop("org_id"))

        slug = d.pop("slug")

        version = d.pop("version")

        definition = ApplicationStandardDefinition.from_dict(d.pop("definition"))

        definition_hash = d.pop("definition_hash")

        description = d.pop("description")

        created_by = UUID(d.pop("created_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        application_standard_version = cls(
            standard_id=standard_id,
            org_id=org_id,
            slug=slug,
            version=version,
            definition=definition,
            definition_hash=definition_hash,
            description=description,
            created_by=created_by,
            created_at=created_at,
        )

        application_standard_version.additional_properties = d
        return application_standard_version

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
