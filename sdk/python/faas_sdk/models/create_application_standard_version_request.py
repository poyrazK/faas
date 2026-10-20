from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_definition import ApplicationStandardDefinition


T = TypeVar("T", bound="CreateApplicationStandardVersionRequest")


@_attrs_define
class CreateApplicationStandardVersionRequest:
    """A strictly decoded candidate publication with an explicit concurrency version."""

    expected_version: int
    definition: ApplicationStandardDefinition
    """Supported versioned application requirements. Resource UUIDs must belong to the organization.
    Enforced destination, publisher and CIDR sets cannot be empty.
    Empty local CIDRs mean unrestricted access and cannot satisfy a restriction.
    """
    description: str | Unset = UNSET
    """UTF-8 description limited to 512 bytes."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        definition = self.definition.to_dict()

        description = self.description

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "definition": definition,
            }
        )
        if description is not UNSET:
            field_dict["description"] = description

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_definition import ApplicationStandardDefinition

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        definition = ApplicationStandardDefinition.from_dict(d.pop("definition"))

        description = d.pop("description", UNSET)

        create_application_standard_version_request = cls(
            expected_version=expected_version,
            definition=definition,
            description=description,
        )

        return create_application_standard_version_request
